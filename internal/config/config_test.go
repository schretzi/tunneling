package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig writes body to a temp file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
	return path
}

// legacyConfig is the pre-1.0 file shape: no bindAddress, no daemon block,
// unquoted integer ports, camelCase keys. It must keep loading unchanged.
const legacyConfig = `
tunnels:
  jump-dev:
    remoteHost: development-jumphost
    remotePort: 22
    localPort: 10022
    kind: gcp
    project: my-project
    zone: europe-west4-a
    nic: nic0
  k8s-dev:
    tunnelHost: localhost
    tunnelPort: 10022
    remoteHost: 10.0.0.1
    remotePort: 443
    localPort: 10443
    kind: ssh
    user: someone
`

func TestLoadLegacyConfig(t *testing.T) {
	cfg, err := Load(writeConfig(t, legacyConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got, want := len(cfg.Tunnels), 2; got != want {
		t.Fatalf("tunnels = %d, want %d", got, want)
	}
	if got := cfg.BindAddress; got != DefaultBindAddress {
		t.Errorf("BindAddress = %q, want %q", got, DefaultBindAddress)
	}
	if got := cfg.Daemon.Log.Path; got != DefaultLogPath() {
		t.Errorf("Daemon.Log.Path = %q, want %q", got, DefaultLogPath())
	}

	gcp := cfg.Tunnels["jump-dev"]
	if gcp.Name != "jump-dev" {
		t.Errorf("Name = %q, want %q", gcp.Name, "jump-dev")
	}
	if gcp.Kind != KindGCP {
		t.Errorf("Kind = %q, want %q", gcp.Kind, KindGCP)
	}
	if got, want := gcp.Endpoint(), "development-jumphost:22"; got != want {
		t.Errorf("Endpoint() = %q, want %q", got, want)
	}
	if got, want := cfg.LocalAddr(gcp), "127.0.0.1:10022"; got != want {
		t.Errorf("LocalAddr() = %q, want %q", got, want)
	}

	ssh := cfg.Tunnels["k8s-dev"]
	if got, want := ssh.SSHServer(), "someone@localhost:10022"; got != want {
		t.Errorf("SSHServer() = %q, want %q", got, want)
	}
}

// Ports may be written unquoted (an int) or quoted (a string); viper coerced
// both, so both have to keep working.
func TestPortAcceptsIntAndString(t *testing.T) {
	const quoted = `
tunnels:
  a:
    kind: ssh
    tunnelHost: localhost
    tunnelPort: "22"
    remoteHost: example.internal
    remotePort: "443"
    localPort: "8443"
`
	cfg, err := Load(writeConfig(t, quoted))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.Tunnels["a"].LocalPort.String(), "8443"; got != want {
		t.Errorf("LocalPort = %q, want %q", got, want)
	}
}

func TestSSHServerWithoutUser(t *testing.T) {
	tun := Tunnel{TunnelHost: "bastion.example", TunnelPort: "2222"}
	if got, want := tun.SSHServer(), "bastion.example:2222"; got != want {
		t.Errorf("SSHServer() = %q, want %q", got, want)
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string // substring the error must contain
	}{
		{
			name: "no tunnels",
			body: "tunnels: {}\n",
			want: "no tunnels configured",
		},
		{
			name: "unknown key",
			body: "tunnels:\n  a:\n    kind: ssh\n    remotehost: x\n",
			want: "field remotehost not found",
		},
		{
			name: "missing kind",
			body: "tunnels:\n  a:\n    remoteHost: x\n    remotePort: 1\n    localPort: 2\n",
			want: "kind is required",
		},
		{
			name: "unknown kind",
			body: "tunnels:\n  a:\n    kind: telnet\n    remoteHost: x\n    remotePort: 1\n    localPort: 2\n",
			want: `unknown kind "telnet"`,
		},
		{
			name: "gcp without project",
			body: "tunnels:\n  a:\n    kind: gcp\n    zone: z\n    nic: nic0\n    remoteHost: x\n    remotePort: 1\n    localPort: 2\n",
			want: "project is required",
		},
		{
			name: "ssh without tunnelHost",
			body: "tunnels:\n  a:\n    kind: ssh\n    remoteHost: x\n    remotePort: 1\n    localPort: 2\n",
			want: "tunnelHost is required",
		},
		{
			name: "port out of range",
			body: "tunnels:\n  a:\n    kind: ssh\n    tunnelHost: h\n    tunnelPort: 22\n    remoteHost: x\n    remotePort: 1\n    localPort: 99999\n",
			want: "out of range",
		},
		{
			name: "port not a number",
			body: "tunnels:\n  a:\n    kind: ssh\n    tunnelHost: h\n    tunnelPort: 22\n    remoteHost: x\n    remotePort: 1\n    localPort: https\n",
			want: "is not a number",
		},
		{
			name: "duplicate local port",
			body: "tunnels:\n  a:\n    kind: ssh\n    tunnelHost: h\n    tunnelPort: 22\n    remoteHost: x\n    remotePort: 1\n    localPort: 8443\n" +
				"  b:\n    kind: ssh\n    tunnelHost: h\n    tunnelPort: 22\n    remoteHost: y\n    remotePort: 1\n    localPort: 8443\n",
			want: "both use localPort 8443",
		},
		{
			name: "bad bind address",
			body: "bindAddress: not-an-ip\ntunnels:\n  a:\n    kind: ssh\n    tunnelHost: h\n    tunnelPort: 22\n    remoteHost: x\n    remotePort: 1\n    localPort: 2\n",
			want: "is not an IP address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			if err == nil {
				t.Fatalf("Load succeeded, want error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil {
		t.Fatal("Load succeeded on a missing file, want error")
	}
}

func TestSortedIsStable(t *testing.T) {
	cfg, err := Load(writeConfig(t, legacyConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Map iteration is randomized, so a single ordered result is only
	// meaningful if it repeats.
	for range 20 {
		got := cfg.Sorted()
		if len(got) != 2 || got[0].Name != "jump-dev" || got[1].Name != "k8s-dev" {
			t.Fatalf("Sorted() = %v, want [jump-dev k8s-dev]", got)
		}
	}
}

func TestSelectTunnels(t *testing.T) {
	cfg, err := Load(writeConfig(t, legacyConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	all, err := cfg.SelectTunnels(nil)
	if err != nil {
		t.Fatalf("SelectTunnels(nil): %v", err)
	}
	if len(all) != 2 {
		t.Errorf("SelectTunnels(nil) returned %d tunnels, want 2", len(all))
	}

	// Order follows the argument, not the config.
	some, err := cfg.SelectTunnels([]string{"k8s-dev", "jump-dev"})
	if err != nil {
		t.Fatalf("SelectTunnels: %v", err)
	}
	if len(some) != 2 || some[0].Name != "k8s-dev" {
		t.Errorf("SelectTunnels = %v, want [k8s-dev jump-dev]", some)
	}

	if _, err := cfg.SelectTunnels([]string{"nope"}); err == nil {
		t.Error("SelectTunnels succeeded on an unknown name, want error")
	}
}

func TestHasKind(t *testing.T) {
	tunnels := []Tunnel{{Kind: KindGCP}}
	if !HasKind(tunnels, KindGCP) {
		t.Error("HasKind(gcp) = false, want true")
	}
	if HasKind(tunnels, KindSSH) {
		t.Error("HasKind(ssh) = true, want false")
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}

	tests := []struct{ in, want string }{
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~", home},
		{"~/.config/tunneling/config.yaml", filepath.Join(home, ".config", "tunneling", "config.yaml")},
		// A "~" that is not a path prefix is left alone.
		{"~otheruser/x", "~otheruser/x"},
	}
	for _, tt := range tests {
		got, err := ExpandPath(tt.in)
		if err != nil {
			t.Errorf("ExpandPath(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ExpandPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// The embedded starter config is what `config init` writes, so it has to
// survive the loader it is written for.
func TestExampleConfigLoads(t *testing.T) {
	cfg, err := Load(writeConfig(t, string(Example)))
	if err != nil {
		t.Fatalf("the embedded example config does not load: %v", err)
	}
	if len(cfg.Tunnels) == 0 {
		t.Error("the embedded example config has no tunnels")
	}
}
