package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schretzi/tunneling/internal/config"
)

// execute runs the root command with args and returns its combined output.
//
// Cobra state is package-global here, so tests in this file must not run in
// parallel; the cleanup restores the flag variables every case relies on.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		configPath = config.DefaultPath()
		tunnels = nil
	})

	err := rootCmd.Execute()
	return buf.String(), err
}

func TestConfigInitAndValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")

	out, err := execute(t, "config", "init", "--config", path)
	if err != nil {
		t.Fatalf("config init: %v (output %q)", err, out)
	}
	if !strings.Contains(out, path) {
		t.Errorf("config init output = %q, want it to name %s", out, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat written config: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config mode = %o, want 600", perm)
	}

	out, err = execute(t, "config", "validate", "--config", path)
	if err != nil {
		t.Fatalf("config validate on a freshly written config: %v (output %q)", err, out)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("config validate output = %q, want it to report ok", out)
	}
}

// `config init` must not clobber a config someone has already edited.
func TestConfigInitRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	const existing = "tunnels: {}\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatalf("seeding config: %v", err)
	}

	if _, err := execute(t, "config", "init", "--config", path); err == nil {
		t.Fatal("config init overwrote an existing file, want error")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading config back: %v", err)
	}
	if string(got) != existing {
		t.Error("config init modified the existing file")
	}
}

func TestConfigValidateReportsBadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("tunnels:\n  a:\n    kind: telnet\n"), 0o600); err != nil {
		t.Fatalf("seeding config: %v", err)
	}

	if _, err := execute(t, "config", "validate", "--config", path); err == nil {
		t.Fatal("config validate accepted an invalid config, want error")
	}
}

func TestStatusReportsClosedPorts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	// Port 1 needs privileges to bind, so nothing local is listening on it.
	const body = `
tunnels:
  closed:
    kind: ssh
    tunnelHost: localhost
    tunnelPort: 22
    remoteHost: example.internal
    remotePort: 443
    localPort: 1
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seeding config: %v", err)
	}

	out, err := execute(t, "status", "--config", path)
	if err == nil {
		t.Error("status exited zero with a closed port, want non-zero")
	}
	if !strings.Contains(out, "CLOSED") {
		t.Errorf("status output = %q, want a CLOSED row", out)
	}
}

// The default --config value must stay a literal "~/..." so generated docs
// and --help do not bake in the building machine's home directory.
func TestDefaultConfigPathIsPortable(t *testing.T) {
	got := rootCmd.PersistentFlags().Lookup("config").DefValue
	if !strings.HasPrefix(got, "~/") {
		t.Errorf("--config default = %q, want a literal ~/ path", got)
	}
}

// Every convention-mandated command must exist and stay spelled the same way;
// scripts and the MacbookSetup docs refer to them by name.
func TestCommandSurface(t *testing.T) {
	want := []string{"config", "daemon", "service", "status", "version"}
	have := make(map[string]bool)
	for _, c := range rootCmd.Commands() {
		have[c.Name()] = true
	}
	for _, name := range want {
		if !have[name] {
			t.Errorf("root command %q is missing", name)
		}
	}

	svc, _, err := rootCmd.Find([]string{"service"})
	if err != nil {
		t.Fatalf("finding service command: %v", err)
	}
	haveSvc := make(map[string]bool)
	for _, c := range svc.Commands() {
		haveSvc[c.Name()] = true
	}
	for _, name := range []string{"install", "uninstall", "start", "stop", "restart", "status"} {
		if !haveSvc[name] {
			t.Errorf("service subcommand %q is missing", name)
		}
	}
}
