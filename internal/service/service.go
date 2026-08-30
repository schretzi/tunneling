// Package service manages this tool's own macOS LaunchAgent: the plist that
// runs it in the background, and the launchctl calls that load, unload and
// inspect it.
//
// "service" always means the launchd job. The foreground process that job
// runs is a separate command (`daemon`, `proxy run`, ...) — see
// MacbookSetup/CONVENTIONS.md, which this package implements:
//
//	label   com.schretzi.<name>
//	plist   ~/Library/LaunchAgents/com.schretzi.<name>.plist
//	log     ~/Library/Logs/<name>.log       (written by the process itself)
//	stderr  ~/Library/Logs/<name>.err.log   (launchd; panics only)
//
// # Testing
//
// Everything that talks to launchd goes through the Service.run field rather
// than calling launchctl directly, so the tests drive Install/Start/Stop/
// Restart/Status against a scripted launchd. That indirection exists for one
// concrete reason: this package was at 19% coverage, with every job-mutating
// method untested, and that is exactly where the bootout/bootstrap race hid
// for as long as it did — `service install` failing intermittently with
// "Bootstrap failed: 5: Input/output error", mostly on the runs that had just
// changed the plist.
//
// runLaunchctl — the one function that actually execs launchctl — stays
// untested on purpose. Covering it would mean either loading real launchd
// jobs from the test suite (which mutates the machine running it) or wrapping
// exec in another layer of indirection that itself would not be tested. The
// argument construction and the output parsing around it are covered; the
// exec call is three lines with nothing to get wrong that a fake would catch.
//
// This file is duplicated verbatim in KerberosKeepAlive, OauthMailToken,
// macswitcher and tunneling. They are four separate modules with no shared
// dependency; keep the copies in sync by hand.
package service

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"
)

// LabelPrefix is the reverse-DNS owner every job of mine shares. It is a
// constant, not the current username: a per-user label changes between
// machines and makes the job impossible to refer to in documentation.
const LabelPrefix = "com.schretzi"

const plistTemplateSrc = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label | xmlesc}}</string>

    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath | xmlesc}}</string>
{{- range .Args}}
        <string>{{. | xmlesc}}</string>
{{- end}}
    </array>
{{- if .Env}}

    <key>EnvironmentVariables</key>
    <dict>
{{- range .Env}}
        <key>{{.Key | xmlesc}}</key>
        <string>{{.Value | xmlesc}}</string>
{{- end}}
    </dict>
{{- end}}

    <key>RunAtLoad</key>
    <true/>

    <!-- Not <true/>: that form restarts the job even after a clean exit, so
         a deliberate stop is impossible without bootout. -->
    <key>KeepAlive</key>
    <dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>

    <key>WorkingDirectory</key>
    <string>{{.HomeDir | xmlesc}}</string>

    <!-- Crash capture only. The process writes its own log to
         {{.LogPath}} through internal/logfile, which reopens
         after newsyslog rotates it. This file receives Go panics and
         anything emitted before logging is set up, so it stays tiny.
         stdout is left unset, and so goes to /dev/null. -->
    <key>StandardErrorPath</key>
    <string>{{.ErrLogPath | xmlesc}}</string>

    <key>ProcessType</key>
    <string>Background</string>

    <key>ThrottleInterval</key>
    <integer>10</integer>
</dict>
</plist>
`

var plistTemplate = template.Must(template.New("launchagent").Funcs(template.FuncMap{
	"xmlesc": func(s string) string {
		var buf bytes.Buffer
		_ = xml.EscapeText(&buf, []byte(s))
		return buf.String()
	},
}).Parse(plistTemplateSrc))

// envPair is a rendered EnvironmentVariables entry. A slice, not a map, so
// the plist comes out in a stable order and does not churn on reinstall.
type envPair struct{ Key, Value string }

type plistData struct {
	Label      string
	BinaryPath string
	Args       []string
	Env        []envPair
	HomeDir    string
	LogPath    string
	ErrLogPath string
}

// Service describes one launchd job owned by this tool.
type Service struct {
	// run invokes launchctl. It is a field rather than a direct call so
	// tests can drive Install/Stop/Start/Status against a scripted launchd
	// instead of the real one — without it, everything that actually
	// manipulates a job is untestable, which is precisely where the
	// bootout/bootstrap race hid.
	run func(args ...string) (string, error)
	// bootoutTimeout and bootoutPollInterval bound the wait in Stop. Fields,
	// not constants, so a test can shrink them; a test for "the job never
	// unloads" would otherwise take the full timeout.
	bootoutTimeout      time.Duration
	bootoutPollInterval time.Duration

	// name is the short job name: the binary name, lower-case. It drives the
	// label, the plist filename and both log paths.
	name string
	// args are the ProgramArguments after the binary itself, i.e. the
	// subcommand that runs in the foreground.
	args []string
	// env is an optional EnvironmentVariables dict, ordered.
	env []envPair
	// binary overrides the executable path written into the plist. Empty
	// means "the running binary" (see BinaryPath).
	binary string
}

// New returns the Service for a job named name whose launchd job runs
// `<binary> args...`.
//
// Anything that is only known after flag parsing — a --config path, the
// location of a helper binary — must not be baked in here, because commands
// are built during init(). Set it from the PrepareFunc passed to NewCommand,
// which runs inside RunE.
func New(name string, args ...string) *Service {
	return &Service{
		name:                name,
		args:                args,
		run:                 runLaunchctl,
		bootoutTimeout:      defaultBootoutTimeout,
		bootoutPollInterval: defaultBootoutPollInterval,
	}
}

// WithArgs replaces the ProgramArguments that follow the binary.
func (s *Service) WithArgs(args ...string) *Service {
	s.args = args
	return s
}

// WithEnv adds an EnvironmentVariables entry to the generated plist. Order of
// calls is preserved.
func (s *Service) WithEnv(key, value string) *Service {
	s.env = append(s.env, envPair{Key: key, Value: value})
	return s
}

// WithBinary pins the executable path written into the plist, overriding the
// running binary. Used by the `--binary` flag.
func (s *Service) WithBinary(path string) *Service {
	s.binary = path
	return s
}

// Name returns the job's short name.
func (s *Service) Name() string { return s.name }

// Label returns the launchd label, e.g. "com.schretzi.macswitcher".
func (s *Service) Label() string { return LabelPrefix + "." + s.name }

// PlistPath returns ~/Library/LaunchAgents/<label>.plist.
func (s *Service) PlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", s.Label()+".plist"), nil
}

// LogDir returns ~/Library/Logs. Logs live directly in it — no per-project
// subdirectory.
func LogDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, "Library", "Logs"), nil
}

// LogPath returns ~/Library/Logs/<name>.log, the log the process writes
// itself.
func (s *Service) LogPath() (string, error) {
	dir, err := LogDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, s.name+".log"), nil
}

// ErrLogPath returns ~/Library/Logs/<name>.err.log, launchd's stderr capture.
func (s *Service) ErrLogPath() (string, error) {
	dir, err := LogDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, s.name+".err.log"), nil
}

// BinaryPath returns the executable path to write into the plist.
//
// It deliberately does *not* resolve symlinks. A Homebrew cask installs the
// real binary under /opt/homebrew/Caskroom/<name>/<version>/ and symlinks it
// into /opt/homebrew/bin; resolving would pin the plist to a version that the
// next `brew upgrade` deletes. The invoked path is the stable one.
func (s *Service) BinaryPath() (string, error) {
	if s.binary != "" {
		return filepath.Abs(s.binary)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolving own executable path: %w", err)
	}
	if isGoBuildPath(exe) {
		return "", fmt.Errorf(
			"refusing to install a LaunchAgent pointing at a temporary `go run` binary (%s):\n"+
				"  install %s first (make build && cp %s /opt/homebrew/bin/, or brew install), or\n"+
				"  pass --binary /path/to/%s", exe, s.name, s.name, s.name,
		)
	}
	return filepath.Abs(exe)
}

// isGoBuildPath reports whether path is one of `go run`'s throwaway binaries
// under the build cache, which disappears the moment the process exits.
func isGoBuildPath(path string) bool {
	return strings.Contains(path, string(filepath.Separator)+"go-build")
}

func guiDomain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func (s *Service) serviceTarget() string { return guiDomain() + "/" + s.Label() }

// launchctlTimeout bounds a single launchctl call. These normally return in
// milliseconds, but launchd can wedge, and a hung `service status` inside a
// provisioning run is much harder to diagnose than a timeout.
const launchctlTimeout = 10 * time.Second

// runLaunchctl runs launchctl and returns its combined output.
func runLaunchctl(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), launchctlTimeout)
	defer cancel()

	// #nosec G204 -- fixed binary name; args are our own constructed strings
	// (domain, label, plist path), never a shell string, so there is no
	// injection surface.
	cmd := exec.CommandContext(ctx, "launchctl", args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if ctx.Err() != nil {
		return strings.TrimSpace(buf.String()),
			fmt.Errorf("launchctl %s timed out after %s: %w", strings.Join(args, " "), launchctlTimeout, ctx.Err())
	}
	return strings.TrimSpace(buf.String()), err
}

// Render returns the plist that Install would write. Exported so tests and
// `service status` can show it without touching the filesystem.
func (s *Service) Render() ([]byte, error) {
	binPath, err := s.BinaryPath()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("determining home directory: %w", err)
	}
	logPath, err := s.LogPath()
	if err != nil {
		return nil, err
	}
	errLogPath, err := s.ErrLogPath()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	err = plistTemplate.Execute(&buf, plistData{
		Label:      s.Label(),
		BinaryPath: binPath,
		Args:       s.args,
		Env:        s.env,
		HomeDir:    home,
		LogPath:    logPath,
		ErrLogPath: errLogPath,
	})
	if err != nil {
		return nil, fmt.Errorf("rendering plist: %w", err)
	}
	return buf.Bytes(), nil
}

// Install writes the plist and loads it. It is idempotent: an already-loaded
// job is unloaded first.
// Install writes the plist and makes sure the job is loaded from it. It
// reports whether anything actually changed, so callers - and the Ansible
// roles behind them - can tell a real install from a no-op.
func (s *Service) Install() (changed bool, err error) {
	rendered, err := s.Render()
	if err != nil {
		return false, err
	}

	logDir, err := LogDir()
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return false, fmt.Errorf("creating log directory %s: %w", logDir, err)
	}

	plistPath, err := s.PlistPath()
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o750); err != nil {
		return false, fmt.Errorf("creating LaunchAgents directory: %w", err)
	}
	// Nothing to do if the plist on disk is already what we would write and
	// the job is running. Reporting that is not cosmetic: a provisioning run
	// calls this every time, and a tool that always claims to have changed
	// something makes "a second run reports no changes" impossible to hold.
	//
	// Both halves matter. Same plist but not loaded means a machine that was
	// rebooted out of a disabled state; loaded but a different plist means the
	// job is running yesterday's configuration.
	//
	// #nosec G304 -- plistPath is PlistPath(), built from the fixed
	// LaunchAgents directory and this service's own label; it is the same
	// path WriteFile below writes to, and never comes from user input.
	if existing, err := os.ReadFile(plistPath); err == nil &&
		bytes.Equal(existing, rendered) && s.Loaded() {
		return false, nil
	}

	if err := os.WriteFile(plistPath, rendered, 0o600); err != nil {
		return false, fmt.Errorf("writing plist %s: %w", plistPath, err)
	}

	if err := s.Stop(); err != nil {
		return false, err
	}
	return true, s.Start()
}

// Uninstall unloads the job and removes its plist. Logs are left alone.
func (s *Service) Uninstall() error {
	if err := s.Stop(); err != nil {
		return err
	}
	plistPath, err := s.PlistPath()
	if err != nil {
		return err
	}
	if err := os.Remove(plistPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing plist %s: %w", plistPath, err)
	}
	return nil
}

// Start bootstraps the job into launchd.
func (s *Service) Start() error {
	plistPath, err := s.PlistPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(plistPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s is not installed (no plist at %s); run `%s service install` first", s.Label(), plistPath, s.name)
		}
		return err
	}
	if out, err := s.run("bootstrap", guiDomain(), plistPath); err != nil {
		return fmt.Errorf("launchctl bootstrap %s: %w: %s", s.Label(), err, out)
	}
	return nil
}

// Defaults for the Stop wait; see the Service fields of the same name.
const (
	defaultBootoutTimeout      = 10 * time.Second
	defaultBootoutPollInterval = 100 * time.Millisecond
)

// Stop unloads the job and waits for launchd to finish. Not being loaded is
// not an error.
func (s *Service) Stop() error {
	if !s.Loaded() {
		return nil
	}
	out, err := s.run("bootout", s.serviceTarget())

	// The wait is the fix, not a nicety. bootout returns once the unload has
	// been *requested*, not once launchd has finished it, so bootstrapping
	// immediately afterwards fails with the famously unhelpful "Bootstrap
	// failed: 5: Input/output error". KeepAlive widens the window, because
	// launchd keeps respawning the job while it is being torn down — which
	// makes `service install` and `service restart` fail *intermittently*,
	// mostly on the runs that changed the plist, i.e. exactly the runs that
	// need to work.
	//
	// It also subsumes the old race check: a bootout that reported an error
	// but did unload the job still counts as success.
	if s.waitUnloaded() {
		return nil
	}
	if err != nil {
		return fmt.Errorf("launchctl bootout %s: %w: %s", s.Label(), err, out)
	}
	return fmt.Errorf("launchctl bootout %s: still loaded after %s", s.Label(), s.bootoutTimeout)
}

// waitUnloaded polls until launchd no longer has the job registered, and
// reports whether it went away within bootoutTimeout.
func (s *Service) waitUnloaded() bool {
	deadline := time.Now().Add(s.bootoutTimeout)
	for {
		if !s.Loaded() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(s.bootoutPollInterval)
	}
}

// Restart stops and starts the job.
func (s *Service) Restart() error {
	if err := s.Stop(); err != nil {
		return err
	}
	return s.Start()
}

// Loaded reports whether launchd currently has the job registered.
func (s *Service) Loaded() bool {
	_, err := s.run("print", s.serviceTarget())
	return err == nil
}

// Status is a point-in-time view of the job.
type Status struct {
	Label        string
	PlistPath    string
	LogPath      string
	ErrLogPath   string
	Installed    bool // a plist exists at the standard path
	Loaded       bool // launchd has it registered
	Running      bool // it has a live pid right now
	PID          int
	LastExitCode int
}

var (
	pidPattern      = regexp.MustCompile(`(?m)^\s*pid = (\d+)`)
	lastExitPattern = regexp.MustCompile(`(?m)^\s*last exit code = (-?\d+)`)
)

// Status inspects the job via `launchctl print`.
func (s *Service) Status() (Status, error) {
	st := Status{Label: s.Label()}

	var err error
	if st.PlistPath, err = s.PlistPath(); err != nil {
		return st, err
	}
	if st.LogPath, err = s.LogPath(); err != nil {
		return st, err
	}
	if st.ErrLogPath, err = s.ErrLogPath(); err != nil {
		return st, err
	}
	if _, statErr := os.Stat(st.PlistPath); statErr == nil {
		st.Installed = true
	}

	// A failing `launchctl print` is the normal answer for a job that is not
	// loaded, not an error to report: Loaded/Running stay false and that is
	// exactly what the caller asked. Returning printErr here would make
	// `service status` exit non-zero for the most ordinary state there is.
	out, printErr := s.run("print", s.serviceTarget())
	if printErr != nil {
		return st, nil //nolint:nilerr // not loaded is a status, not a failure
	}
	st.Loaded = true
	if m := pidPattern.FindStringSubmatch(out); m != nil {
		if pid, convErr := strconv.Atoi(m[1]); convErr == nil {
			st.PID = pid
			st.Running = pid > 0
		}
	}
	if m := lastExitPattern.FindStringSubmatch(out); m != nil {
		if code, convErr := strconv.Atoi(m[1]); convErr == nil {
			st.LastExitCode = code
		}
	}
	return st, nil
}
