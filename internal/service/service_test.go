package service

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLabelAndPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s := New("widget", "daemon")

	if got, want := s.Label(), "com.schretzi.widget"; got != want {
		t.Errorf("Label() = %q, want %q", got, want)
	}

	for _, tc := range []struct {
		name string
		got  func() (string, error)
		want string
	}{
		{"PlistPath", s.PlistPath, filepath.Join(home, "Library", "LaunchAgents", "com.schretzi.widget.plist")},
		{"LogPath", s.LogPath, filepath.Join(home, "Library", "Logs", "widget.log")},
		{"ErrLogPath", s.ErrLogPath, filepath.Join(home, "Library", "Logs", "widget.err.log")},
	} {
		got, err := tc.got()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestRenderDoesNotResolveSymlinks is the regression guard for the pinned-path
// bug: resolving symlinks turns /opt/homebrew/bin/x into
// /opt/homebrew/Caskroom/x/<version>/x, which the next `brew upgrade` deletes.
func TestRenderDoesNotResolveSymlinks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	versionedDir := filepath.Join(dir, "Caskroom", "widget", "1.2.3")
	if err := os.MkdirAll(versionedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	realBin := filepath.Join(versionedDir, "widget")
	if err := os.WriteFile(realBin, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stableBin := filepath.Join(dir, "widget")
	if err := os.Symlink(realBin, stableBin); err != nil {
		t.Fatal(err)
	}

	out, err := New("widget", "daemon").WithBinary(stableBin).Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	plist := string(out)

	if !strings.Contains(plist, stableBin) {
		t.Errorf("plist does not reference the stable path %s:\n%s", stableBin, plist)
	}
	if strings.Contains(plist, "Caskroom") {
		t.Errorf("plist resolved through to the version-pinned path:\n%s", plist)
	}
}

func TestRenderContents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	bin := filepath.Join(t.TempDir(), "widget")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := New("widget", "daemon", "--config", "/etc/widget.yaml").
		WithBinary(bin).
		WithEnv("WIDGET_HELPER", "/opt/homebrew/bin/helper").
		Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	plist := string(out)

	for _, want := range []string{
		"<string>com.schretzi.widget</string>",
		"<string>daemon</string>",
		"<string>--config</string>",
		"<string>/etc/widget.yaml</string>",
		"<key>WIDGET_HELPER</key>",
		"<string>/opt/homebrew/bin/helper</string>",
		filepath.Join(home, "Library", "Logs", "widget.err.log"),
		"<key>ProcessType</key>",
		"<key>ThrottleInterval</key>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q:\n%s", want, plist)
		}
	}

	// KeepAlive must be the SuccessfulExit dict, never a bare <true/>, or a
	// deliberate `service stop` is impossible.
	if !strings.Contains(plist, "<key>SuccessfulExit</key>") {
		t.Errorf("plist does not use the KeepAlive/SuccessfulExit form:\n%s", plist)
	}

	// stdout is intentionally unset so it goes to /dev/null; only stderr is
	// captured, for panics.
	if strings.Contains(plist, "StandardOutPath") {
		t.Errorf("plist sets StandardOutPath; only StandardErrorPath is expected:\n%s", plist)
	}
}

func TestRenderEscapesXML(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	out, err := New("widget", `--flag=a&b<c>`).WithBinary("/tmp/widget").Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	plist := string(out)

	if strings.Contains(plist, "a&b<c>") {
		t.Errorf("argument was not XML-escaped:\n%s", plist)
	}
	if !strings.Contains(plist, "a&amp;b&lt;c&gt;") {
		t.Errorf("escaped argument not found:\n%s", plist)
	}
}

func TestBinaryPathRejectsGoRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := New("widget").
		WithBinary("").
		Render()
	// Whether this errors depends on how the test binary itself was built, so
	// assert on the helper instead, which is what BinaryPath keys off.
	_ = err

	if !isGoBuildPath("/var/folders/xy/T/go-build123/b001/exe/widget") {
		t.Error("isGoBuildPath did not recognise a `go run` binary path")
	}
	if isGoBuildPath("/opt/homebrew/bin/widget") {
		t.Error("isGoBuildPath flagged an installed binary")
	}
}

func TestWithEnvPreservesOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	out, err := New("widget").
		WithBinary("/tmp/widget").
		WithEnv("FIRST", "1").
		WithEnv("SECOND", "2").
		Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	plist := string(out)

	first := strings.Index(plist, "FIRST")
	second := strings.Index(plist, "SECOND")
	if first < 0 || second < 0 {
		t.Fatalf("both env keys should be present:\n%s", plist)
	}
	if first > second {
		t.Error("env entries are not in insertion order; the plist will churn between reinstalls")
	}
}

// --- launchd interaction -----------------------------------------------
//
// Everything below drives Install/Start/Stop/Restart/Status against a
// scripted launchctl rather than the real one. Without the injection point
// these are the methods that cannot be tested at all — and they are exactly
// where the bootout/bootstrap race lived.

// fakeLaunchctl records the launchctl invocations a test provokes and answers
// them from a script.
type fakeLaunchctl struct {
	calls []string
	// loaded is consulted by "print": true means the job is registered.
	loaded bool
	// printOutput is what a successful "print" returns.
	printOutput string
	// unloadAfter is how many "print" calls after a "bootout" still report
	// the job as loaded, simulating launchd's asynchronous teardown.
	unloadAfter int
	// failBootout makes "bootout" report an error, without changing whether
	// the job actually unloads.
	failBootout bool
	// failBootstrap makes "bootstrap" report an error.
	failBootstrap bool

	printsSinceBootout int
	bootoutSeen        bool
}

func (f *fakeLaunchctl) run(args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch args[0] {
	case "print":
		if f.bootoutSeen {
			f.printsSinceBootout++
			if f.printsSinceBootout > f.unloadAfter {
				f.loaded = false
			}
		}
		if !f.loaded {
			return "", errors.New("Could not find service")
		}
		return f.printOutput, nil
	case "bootout":
		f.bootoutSeen = true
		if f.unloadAfter == 0 {
			f.loaded = false
		}
		if f.failBootout {
			return "Boot-out failed", errors.New("exit status 3")
		}
		return "", nil
	case "bootstrap":
		if f.failBootstrap {
			return "Bootstrap failed: 5: Input/output error", errors.New("exit status 5")
		}
		// Loading again ends the previous teardown, so the unload countdown
		// has to reset. Without this the job would appear to unload itself
		// on the next print after any earlier bootout.
		f.loaded = true
		f.bootoutSeen, f.printsSinceBootout = false, 0
		return "", nil
	default:
		return "", nil
	}
}

// newTestService returns a Service wired to fake, with the Stop wait shrunk so
// a test that never unloads finishes quickly.
func newTestService(t *testing.T, fake *fakeLaunchctl) *Service {
	t.Helper()
	s := New("widget", "daemon")
	s.run = fake.run
	s.bootoutTimeout = 200 * time.Millisecond
	s.bootoutPollInterval = time.Millisecond
	return s
}

// installedService additionally writes the plist Start expects to find.
func installedService(t *testing.T, fake *fakeLaunchctl) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	bin := filepath.Join(t.TempDir(), "widget")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newTestService(t, fake).WithBinary(bin)
	return s, home
}

// The regression test for "Bootstrap failed: 5: Input/output error".
//
// bootout returns once the unload has been *requested*. Stop must keep
// looking until the label is actually gone, or the Start that follows in
// Install/Restart bootstraps into a job launchd is still tearing down.
func TestStopWaitsForTheUnloadToComplete(t *testing.T) {
	fake := &fakeLaunchctl{loaded: true, unloadAfter: 3}
	s := newTestService(t, fake)

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if fake.loaded {
		t.Error("Stop returned while the job was still loaded")
	}
	// It must have kept polling rather than trusting bootout's return.
	if fake.printsSinceBootout < 3 {
		t.Errorf("only %d print(s) after bootout; Stop did not wait", fake.printsSinceBootout)
	}
}

// A bootout that reports an error but did unload the job is a success: that
// race is the reason the check exists.
func TestStopTreatsAnUnloadedJobAsSuccessDespiteError(t *testing.T) {
	fake := &fakeLaunchctl{loaded: true, failBootout: true}
	if err := newTestService(t, fake).Stop(); err != nil {
		t.Errorf("Stop reported an error for a job that did unload: %v", err)
	}
}

func TestStopFailsWhenTheJobNeverUnloads(t *testing.T) {
	// unloadAfter far beyond what the shrunk timeout allows.
	fake := &fakeLaunchctl{loaded: true, unloadAfter: 1 << 30}
	err := newTestService(t, fake).Stop()
	if err == nil {
		t.Fatal("Stop succeeded although the job stayed loaded")
	}
	if !strings.Contains(err.Error(), "still loaded") {
		t.Errorf("error = %v, want it to say the job is still loaded", err)
	}
}

func TestStopOnAnUnloadedJobDoesNothing(t *testing.T) {
	fake := &fakeLaunchctl{loaded: false}
	if err := newTestService(t, fake).Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	for _, call := range fake.calls {
		if strings.HasPrefix(call, "bootout") {
			t.Errorf("Stop booted out a job that was not loaded: %v", fake.calls)
		}
	}
}

// Install is documented as idempotent, and that is how a plist change is
// applied: it must unload before it loads.
func TestInstallUnloadsThenLoads(t *testing.T) {
	fake := &fakeLaunchctl{loaded: true}
	s, home := installedService(t, fake)

	if _, err := s.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.schretzi.widget.plist")
	info, err := os.Stat(plistPath)
	if err != nil {
		t.Fatalf("Install did not write the plist: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("plist mode = %o, want 600", perm)
	}

	var bootout, bootstrap int
	for i, call := range fake.calls {
		switch {
		case strings.HasPrefix(call, "bootout"):
			bootout = i + 1
		case strings.HasPrefix(call, "bootstrap"):
			bootstrap = i + 1
		}
	}
	if bootout == 0 || bootstrap == 0 {
		t.Fatalf("Install did not both unload and load: %v", fake.calls)
	}
	if bootout > bootstrap {
		t.Errorf("Install bootstrapped before booting out: %v", fake.calls)
	}
}

func TestStartRefusesWithoutAPlist(t *testing.T) {
	fake := &fakeLaunchctl{}
	s, _ := installedService(t, fake)

	err := s.Start()
	if err == nil {
		t.Fatal("Start succeeded with no plist installed")
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Errorf("error = %v, want it to say the job is not installed", err)
	}
	for _, call := range fake.calls {
		if strings.HasPrefix(call, "bootstrap") {
			t.Error("Start bootstrapped despite there being no plist")
		}
	}
}

func TestStartSurfacesBootstrapFailure(t *testing.T) {
	fake := &fakeLaunchctl{failBootstrap: true}
	s, _ := installedService(t, fake)
	if _, err := s.Install(); err == nil {
		t.Fatal("Install succeeded although bootstrap failed")
	} else if !strings.Contains(err.Error(), "Input/output error") {
		t.Errorf("error = %v, want launchctl's message included", err)
	}
}

func TestUninstallRemovesThePlist(t *testing.T) {
	fake := &fakeLaunchctl{loaded: true}
	s, home := installedService(t, fake)
	if _, err := s.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if err := s.Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.schretzi.widget.plist")
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Errorf("plist still present after Uninstall (stat err = %v)", err)
	}
	// Logs are documented as left in place.
	if _, err := os.Stat(filepath.Join(home, "Library", "Logs")); err != nil {
		t.Errorf("Uninstall removed the log directory: %v", err)
	}
}

// Uninstalling something that was never installed is not an error.
func TestUninstallIsIdempotent(t *testing.T) {
	fake := &fakeLaunchctl{}
	s, _ := installedService(t, fake)
	if err := s.Uninstall(); err != nil {
		t.Errorf("Uninstall on a job that was never installed: %v", err)
	}
}

func TestStatusParsesLaunchctlPrint(t *testing.T) {
	fake := &fakeLaunchctl{
		loaded: true,
		printOutput: `
	state = running
	pid = 4321
	last exit code = 0
`,
	}
	s, _ := installedService(t, fake)
	if _, err := s.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}

	st, err := s.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Installed || !st.Loaded || !st.Running {
		t.Errorf("Status = %+v, want installed/loaded/running", st)
	}
	if st.PID != 4321 {
		t.Errorf("PID = %d, want 4321", st.PID)
	}
	if st.Label != "com.schretzi.widget" {
		t.Errorf("Label = %q", st.Label)
	}
}

// A job that is not loaded is the most ordinary state there is. Reporting it
// must not be an error, or `service status` exits non-zero all the time.
func TestStatusOnAnUnloadedJobIsNotAnError(t *testing.T) {
	fake := &fakeLaunchctl{loaded: false}
	s, _ := installedService(t, fake)

	st, err := s.Status()
	if err != nil {
		t.Fatalf("Status on an unloaded job returned an error: %v", err)
	}
	if st.Loaded || st.Running {
		t.Errorf("Status = %+v, want neither loaded nor running", st)
	}
}

func TestStatusReportsLastExitCode(t *testing.T) {
	fake := &fakeLaunchctl{
		loaded:      true,
		printOutput: "\tlast exit code = 78\n",
	}
	s, _ := installedService(t, fake)

	st, err := s.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Running {
		t.Error("Status reports running with no pid line")
	}
	if st.LastExitCode != 78 {
		t.Errorf("LastExitCode = %d, want 78", st.LastExitCode)
	}
}

func TestRestartStopsThenStarts(t *testing.T) {
	fake := &fakeLaunchctl{loaded: true}
	s, _ := installedService(t, fake)
	if _, err := s.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	fake.calls = nil

	if err := s.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	var order []string
	for _, call := range fake.calls {
		if verb := strings.Fields(call)[0]; verb == "bootout" || verb == "bootstrap" {
			order = append(order, verb)
		}
	}
	if len(order) != 2 || order[0] != "bootout" || order[1] != "bootstrap" {
		t.Errorf("Restart issued %v, want [bootout bootstrap]", order)
	}
}

func TestWriteStatusRendersEveryField(t *testing.T) {
	var buf bytes.Buffer
	WriteStatus(&buf, Status{
		Label: "com.schretzi.widget", Installed: true, Loaded: true,
		Running: true, PID: 99,
		PlistPath: "/p", LogPath: "/l", ErrLogPath: "/e",
	})
	for _, want := range []string{"com.schretzi.widget", "installed  yes", "running    yes (pid 99)", "/p", "/l", "/e"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("WriteStatus output missing %q:\n%s", want, buf.String())
		}
	}

	// A loaded-but-not-running job is the crash-loop case: the last exit code
	// is the only clue, so it must be shown.
	buf.Reset()
	WriteStatus(&buf, Status{Loaded: true, Running: false, LastExitCode: 2})
	if !strings.Contains(buf.String(), "last exit  2") {
		t.Errorf("WriteStatus omitted the last exit code:\n%s", buf.String())
	}
}

// --- the `service` command tree ----------------------------------------
//
// Every tool exposes this same surface, so the verbs and their output shape
// are part of the contract, not an implementation detail.

// runCmd executes the service subtree with args and returns its output.
func runCmd(t *testing.T, s *Service, prepare PrepareFunc, args ...string) (string, error) {
	t.Helper()
	cmd := NewCommand(s, prepare)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	return buf.String(), err
}

func TestCommandTreeHasEveryVerb(t *testing.T) {
	cmd := NewCommand(New("widget"), nil)
	have := make(map[string]bool)
	for _, c := range cmd.Commands() {
		have[c.Name()] = true
	}
	for _, verb := range []string{"install", "uninstall", "start", "stop", "restart", "status"} {
		if !have[verb] {
			t.Errorf("service subcommand %q is missing", verb)
		}
	}
}

func TestCommandStatusPrintsState(t *testing.T) {
	fake := &fakeLaunchctl{loaded: true, printOutput: "\tstate = running\n\tpid = 7\n"}
	s, _ := installedService(t, fake)
	if _, err := s.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}

	out, err := runCmd(t, s, nil, "status")
	if err != nil {
		t.Fatalf("service status: %v", err)
	}
	for _, want := range []string{"com.schretzi.widget", "running    yes (pid 7)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// install must run the PrepareFunc, and must not touch anything when it
// fails: installing a job that cannot start just yields a background
// crash-loop that KeepAlive retries forever.
func TestCommandInstallRefusesWhenPrepareFails(t *testing.T) {
	fake := &fakeLaunchctl{}
	s, home := installedService(t, fake)

	out, err := runCmd(t, s, func(*Service) error {
		return errors.New("config invalid: no tunnels configured")
	}, "install")
	if err == nil {
		t.Fatal("install succeeded although prepare failed")
	}
	if !strings.Contains(err.Error(), "not installing") {
		t.Errorf("error = %v, want it to say nothing was installed", err)
	}
	if !strings.Contains(err.Error(), "no tunnels configured") {
		t.Errorf("error = %v, want the underlying reason", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.schretzi.widget.plist")
	if _, statErr := os.Stat(plistPath); !os.IsNotExist(statErr) {
		t.Errorf("a plist was written despite prepare failing (out %q)", out)
	}
	for _, call := range fake.calls {
		if strings.HasPrefix(call, "bootstrap") {
			t.Error("the job was loaded despite prepare failing")
		}
	}
}

func TestCommandInstallAppliesPrepare(t *testing.T) {
	fake := &fakeLaunchctl{}
	s, home := installedService(t, fake)

	prepare := func(s *Service) error {
		// The real use: arguments only known after flag parsing.
		s.WithArgs("daemon", "--config", "/tmp/widget.yaml")
		return nil
	}
	out, err := runCmd(t, s, prepare, "install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(out, "installed and started") {
		t.Errorf("output = %q, want it to confirm the install", out)
	}

	plist, err := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", "com.schretzi.widget.plist"))
	if err != nil {
		t.Fatalf("reading plist: %v", err)
	}
	if !strings.Contains(string(plist), "/tmp/widget.yaml") {
		t.Errorf("plist does not carry the prepared arguments:\n%s", plist)
	}
}

func TestCommandActionsReportPastTense(t *testing.T) {
	for _, tc := range []struct{ verb, want string }{
		{"start", "started"},
		{"stop", "stopped"},
		{"restart", "restarted"},
		{"uninstall", "uninstalled"},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			fake := &fakeLaunchctl{loaded: true}
			s, _ := installedService(t, fake)
			if _, err := s.Install(); err != nil {
				t.Fatalf("Install: %v", err)
			}

			out, err := runCmd(t, s, nil, tc.verb)
			if err != nil {
				t.Fatalf("service %s: %v", tc.verb, err)
			}
			if !strings.Contains(out, tc.want+" com.schretzi.widget") {
				t.Errorf("output = %q, want %q", out, tc.want+" com.schretzi.widget")
			}
		})
	}
}

// --binary is how you point the plist at a binary other than the running one.
func TestCommandBinaryFlagIsHonoured(t *testing.T) {
	fake := &fakeLaunchctl{}
	home := t.TempDir()
	t.Setenv("HOME", home)

	bin := filepath.Join(t.TempDir(), "elsewhere", "widget")
	if err := os.MkdirAll(filepath.Dir(bin), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newTestService(t, fake)
	if _, err := runCmd(t, s, nil, "install", "--binary", bin); err != nil {
		t.Fatalf("install --binary: %v", err)
	}

	plist, err := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", "com.schretzi.widget.plist"))
	if err != nil {
		t.Fatalf("reading plist: %v", err)
	}
	if !strings.Contains(string(plist), bin) {
		t.Errorf("plist does not point at the --binary path %s:\n%s", bin, plist)
	}
}

func TestCommandRejectsUnexpectedArguments(t *testing.T) {
	s, _ := installedService(t, &fakeLaunchctl{})
	if _, err := runCmd(t, s, nil, "status", "extra"); err == nil {
		t.Error("`service status extra` was accepted, want an argument error")
	}
}

// The rule this exists to keep: a second install changes nothing and says so.
// The Ansible roles call `service install` on every run and key their
// changed_when on the wording, so "installed and started" every time makes
// "a second run reports no changes" impossible to hold.
func TestInstallIsANoOpWhenNothingChanged(t *testing.T) {
	// installedService uses t.Setenv, which rules out t.Parallel.
	fake := &fakeLaunchctl{}
	s, _ := installedService(t, fake)

	changed, err := s.Install()
	if err != nil {
		t.Fatalf("first Install: %v", err)
	}
	if !changed {
		t.Fatal("first Install reported no change")
	}

	changed, err = s.Install()
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if changed {
		t.Fatal("second Install reported a change although the plist and the job are unchanged")
	}
}

// Same plist, but the job is not loaded: that is a machine that came back from
// a reboot with the agent disabled, and it has to be started.
func TestInstallStartsAnUnloadedJobEvenWhenThePlistMatches(t *testing.T) {
	// installedService uses t.Setenv, which rules out t.Parallel.
	fake := &fakeLaunchctl{}
	s, _ := installedService(t, fake)
	if _, err := s.Install(); err != nil {
		t.Fatalf("first Install: %v", err)
	}

	fake.loaded = false // rebooted, nothing running
	changed, err := s.Install()
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if !changed {
		t.Fatal("Install left an unloaded job alone although the plist was already correct")
	}
}
