// Package config loads and validates the tunneling YAML configuration.
//
// The file format is unchanged from the pre-1.0 tool: a `tunnels:` mapping of
// name to tunnel definition, with the same camelCase keys. Everything else
// (the `daemon:` block, `bindAddress`) is optional and defaulted, so an
// existing config keeps loading untouched.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Kind is a tunnel's transport: how the local listener reaches the remote
// host.
type Kind string

const (
	// KindGCP tunnels through Google Cloud Identity-Aware Proxy, straight to
	// a Compute Engine instance's network interface. No bastion, no SSH.
	KindGCP Kind = "gcp"
	// KindSSH tunnels through an SSH server (local port forward), using the
	// keys held by the running ssh-agent.
	KindSSH Kind = "ssh"
)

// Config is the top-level YAML configuration.
type Config struct {
	// BindAddress is the local interface GCP/IAP listeners bind to. It
	// defaults to DefaultBindAddress; set it to "0.0.0.0" to expose the
	// tunnels to the rest of the network.
	//
	// It does not apply to `kind: ssh` tunnels: elliotchance/sshtunnel hard-
	// codes a "localhost:<port>" listener, so those are always loopback-only.
	BindAddress string `yaml:"bindAddress"`

	// HostKeyChecking controls how `kind: ssh` tunnels verify the SSH
	// server's host key, mirroring ssh(1)'s StrictHostKeyChecking. See the
	// HostKey* constants; defaults to HostKeyAcceptNew.
	HostKeyChecking string `yaml:"hostKeyChecking"`

	// KnownHostsPath is the known_hosts file consulted and appended to.
	// Defaults to DefaultKnownHostsPath, i.e. the same file ssh(1) uses.
	KnownHostsPath string `yaml:"knownHostsPath"`

	Daemon DaemonConfig `yaml:"daemon"`

	// Tunnels is keyed by tunnel name. A map rather than a list because that
	// is the shape the existing configs are written in.
	Tunnels map[string]Tunnel `yaml:"tunnels"`
}

// DaemonConfig configures the `daemon` subcommand.
type DaemonConfig struct {
	Log LogConfig `yaml:"log"`
}

// LogConfig configures the daemon's own log file. This is separate from the
// launchd-captured stderr file, which only ever receives crash output (panics,
// and failures before logging is set up).
//
// There are no rotation knobs here: rotation belongs to newsyslog, configured
// in MacbookSetup under etc/newsyslog.d/tunneling.conf. The daemon's only
// obligation is to notice when newsyslog has rotated the file out from under
// it, which internal/logfile handles.
type LogConfig struct {
	Path string `yaml:"path"`
}

// Tunnel is one local listener and where its connections are forwarded to.
//
// Which fields matter depends on Kind:
//
//	gcp  Project, Zone, Nic, RemoteHost (the instance name), RemotePort
//	ssh  TunnelHost, TunnelPort, User, RemoteHost, RemotePort
type Tunnel struct {
	// Name is the tunnel's key in the `tunnels:` mapping, filled in by Load.
	// It has no YAML key of its own.
	Name string `yaml:"-"`

	Kind Kind `yaml:"kind"`

	// RemoteHost is the final destination: a Compute Engine instance name for
	// KindGCP, or a host reachable from the SSH server for KindSSH.
	RemoteHost string `yaml:"remoteHost"`
	RemotePort Port   `yaml:"remotePort"`

	// TunnelHost and TunnelPort are the SSH server to forward through
	// (KindSSH only). They are commonly localhost plus the local port of a
	// `kind: gcp` tunnel, chaining an SSH forward over an IAP tunnel.
	TunnelHost string `yaml:"tunnelHost"`
	TunnelPort Port   `yaml:"tunnelPort"`

	// LocalPort is the port to listen on. Unique across all tunnels.
	LocalPort Port `yaml:"localPort"`

	// Project, Zone and Nic locate the instance for KindGCP.
	Project string `yaml:"project"`
	Zone    string `yaml:"zone"`
	Nic     string `yaml:"nic"`

	// User is the SSH login name (KindSSH). Empty means the local username,
	// as ssh(1) would.
	User string `yaml:"user"`
}

// Port is a TCP port as written in the config. It is a string because that is
// what both the IAP client and elliotchance/sshtunnel take, and it accepts
// both the bare (`22`) and quoted (`"22"`) YAML spellings — the pre-1.0 tool
// loaded through viper, which coerced either.
type Port string

// UnmarshalYAML accepts any scalar and keeps its literal text, so an integer
// and a quoted string parse identically.
func (p *Port) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("port must be a number or a string, got %s", value.Tag)
	}
	*p = Port(value.Value)
	return nil
}

// String returns the port as written, for use in an address.
func (p Port) String() string { return string(p) }

// validate reports whether p is a usable TCP port number.
func (p Port) validate() error {
	n, err := strconv.Atoi(string(p))
	if err != nil {
		return fmt.Errorf("%q is not a number", string(p))
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("%d is out of range 1-65535", n)
	}
	return nil
}

// DefaultBindAddress is the interface GCP/IAP listeners bind to when
// bindAddress is unset.
//
// Loopback, not 0.0.0.0. A tunnel is a hole through a perimeter — an IAP
// listener on 0.0.0.0 hands anyone on the same coffee-shop Wi-Fi a route to a
// production jump host. `kind: ssh` tunnels are loopback-only regardless, so
// this also makes the two kinds behave the same way.
const DefaultBindAddress = "127.0.0.1"

// Host key checking modes for `kind: ssh` tunnels.
//
// Until this was configurable the tool accepted *any* host key without
// recording it, which is what elliotchance/sshtunnel hard-coded. That makes
// every ssh tunnel trivially MITM-able by whatever answers on
// tunnelHost:tunnelPort — and these tunnels carry Kubernetes API
// credentials.
const (
	// HostKeyAcceptNew records the key of a host that known_hosts has never
	// seen, but refuses to connect if a *known* host presents a different
	// key. The default: it protects against interception from the second
	// connection onwards without needing every jump host seeded by hand.
	//
	// The failure worth knowing about: rebuilding a jump host changes its
	// key, and this then refuses to connect until the stale known_hosts line
	// is removed. That is the intended behaviour — it is indistinguishable
	// from an interception — but on hosts that are routinely recreated it is
	// noise, and HostKeyOff is the escape hatch.
	HostKeyAcceptNew = "accept-new"
	// HostKeyStrict refuses any host not already in known_hosts.
	HostKeyStrict = "strict"
	// HostKeyOff accepts any key and records nothing: the pre-1.0 behaviour.
	HostKeyOff = "off"
)

// DefaultKnownHostsPath is the known_hosts file used when none is configured.
func DefaultKnownHostsPath() string { return "~/.ssh/known_hosts" }

// LocalAddr returns the address a tunnel's listener should bind to.
func (c *Config) LocalAddr(t Tunnel) string {
	return net.JoinHostPort(c.BindAddress, t.LocalPort.String())
}

// DialAddr returns the loopback address to probe a tunnel's local port on.
// `status` uses it; it is always loopback, even when BindAddress is wider.
func DialAddr(t Tunnel) string {
	return net.JoinHostPort("127.0.0.1", t.LocalPort.String())
}

// Endpoint returns "host:port" for the tunnel's final destination.
func (t Tunnel) Endpoint() string {
	return net.JoinHostPort(t.RemoteHost, t.RemotePort.String())
}

// SSHAddr returns the "host:port" of the SSH server to forward through.
func (t Tunnel) SSHAddr() string {
	return net.JoinHostPort(t.TunnelHost, t.TunnelPort.String())
}

// SSHServer returns "[user@]host:port" for display and log lines.
func (t Tunnel) SSHServer() string {
	if t.User == "" {
		return t.SSHAddr()
	}
	return t.User + "@" + t.SSHAddr()
}

// DefaultPath returns the conventional config path, written with a literal
// leading "~" so it stays portable in --help output and generated docs
// (ExpandPath resolves it against the real home directory at use time).
func DefaultPath() string {
	return "~/.config/tunneling/config.yaml"
}

// DefaultLogPath returns the conventional daemon log path, written with a
// literal leading "~" for the same reason as DefaultPath.
//
// Flat in ~/Library/Logs and named after the binary, per
// MacbookSetup/CONVENTIONS.md — not a per-project subdirectory.
func DefaultLogPath() string {
	return "~/Library/Logs/tunneling.log"
}

// ExpandPath resolves a leading "~" or "~/..." in path against the current
// user's home directory. Paths without a leading "~" are returned unchanged.
func ExpandPath(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

// Load reads, parses and validates the config file at path (which may start
// with "~/").
func Load(path string) (*Config, error) {
	path, err := ExpandPath(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is the user's own --config flag, reading it is the point
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var cfg Config
	// Unknown keys are an error rather than a silent no-op: a typo like
	// `remoteport:` would otherwise leave the field empty and produce a
	// baffling runtime failure instead of a message naming the line.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config %s: %w", path, err)
	}
	return &cfg, nil
}

// applyDefaults fills in unset optional fields and copies each tunnel's map
// key into its Name.
func (c *Config) applyDefaults() {
	if c.BindAddress == "" {
		c.BindAddress = DefaultBindAddress
	}
	if c.Daemon.Log.Path == "" {
		c.Daemon.Log.Path = DefaultLogPath()
	}
	if c.HostKeyChecking == "" {
		c.HostKeyChecking = HostKeyAcceptNew
	}
	if c.KnownHostsPath == "" {
		c.KnownHostsPath = DefaultKnownHostsPath()
	}
	for name, t := range c.Tunnels {
		t.Name = name
		c.Tunnels[name] = t
	}
}

// Validate checks the config for structural and semantic errors.
func (c *Config) Validate() error {
	if len(c.Tunnels) == 0 {
		return errors.New("no tunnels configured")
	}
	if net.ParseIP(c.BindAddress) == nil {
		return fmt.Errorf("bindAddress %q is not an IP address", c.BindAddress)
	}
	switch c.HostKeyChecking {
	case HostKeyAcceptNew, HostKeyStrict, HostKeyOff:
	default:
		return fmt.Errorf("hostKeyChecking %q is not one of %q, %q, %q",
			c.HostKeyChecking, HostKeyAcceptNew, HostKeyStrict, HostKeyOff)
	}

	// Two tunnels on one port means whichever loses the race to bind takes
	// the whole process down at startup. Catching it here names both.
	byPort := make(map[Port]string, len(c.Tunnels))
	for _, t := range c.Sorted() {
		if err := t.validate(); err != nil {
			return fmt.Errorf("tunnel %q: %w", t.Name, err)
		}
		if other, dup := byPort[t.LocalPort]; dup {
			return fmt.Errorf("tunnels %q and %q both use localPort %s", other, t.Name, t.LocalPort)
		}
		byPort[t.LocalPort] = t.Name
	}
	return nil
}

func (t Tunnel) validate() error {
	if err := t.LocalPort.validate(); err != nil {
		return fmt.Errorf("localPort: %w", err)
	}
	if t.RemoteHost == "" {
		return errors.New("remoteHost is required")
	}
	if err := t.RemotePort.validate(); err != nil {
		return fmt.Errorf("remotePort: %w", err)
	}

	switch t.Kind {
	case KindGCP:
		if t.Project == "" {
			return errors.New("project is required for kind: gcp")
		}
		if t.Zone == "" {
			return errors.New("zone is required for kind: gcp")
		}
		if t.Nic == "" {
			return errors.New("nic is required for kind: gcp")
		}
	case KindSSH:
		if t.TunnelHost == "" {
			return errors.New("tunnelHost is required for kind: ssh")
		}
		if err := t.TunnelPort.validate(); err != nil {
			return fmt.Errorf("tunnelPort: %w", err)
		}
	case "":
		return fmt.Errorf("kind is required, use %q or %q", KindGCP, KindSSH)
	default:
		return fmt.Errorf("unknown kind %q, use %q or %q", t.Kind, KindGCP, KindSSH)
	}
	return nil
}

// Sorted returns every configured tunnel ordered by name, so output and
// startup order are stable rather than following Go's randomized map
// iteration.
func (c *Config) Sorted() []Tunnel {
	names := make([]string, 0, len(c.Tunnels))
	for name := range c.Tunnels {
		names = append(names, name)
	}
	slices.Sort(names)

	tunnels := make([]Tunnel, 0, len(names))
	for _, name := range names {
		tunnels = append(tunnels, c.Tunnels[name])
	}
	return tunnels
}

// SelectTunnels returns the tunnels matching names, in the order given, or
// every configured tunnel (name-sorted) if names is empty.
func (c *Config) SelectTunnels(names []string) ([]Tunnel, error) {
	if len(names) == 0 {
		return c.Sorted(), nil
	}
	selected := make([]Tunnel, 0, len(names))
	for _, n := range names {
		t, ok := c.Tunnels[n]
		if !ok {
			return nil, fmt.Errorf("unknown tunnel %q", n)
		}
		selected = append(selected, t)
	}
	return selected, nil
}

// HasKind reports whether any of tunnels is of the given kind.
func HasKind(tunnels []Tunnel, kind Kind) bool {
	for _, t := range tunnels {
		if t.Kind == kind {
			return true
		}
	}
	return false
}

// ProbeTimeout bounds the dial `status` uses to test whether a tunnel's local
// port is accepting connections. Loopback, so anything slower than this is a
// wedged listener, not a slow one.
const ProbeTimeout = 500 * time.Millisecond
