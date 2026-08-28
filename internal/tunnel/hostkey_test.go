package tunnel

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/schretzi/tunneling/internal/config"

	"golang.org/x/crypto/ssh"
)

// testKey generates a throwaway host key.
func testKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	signer, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("converting key: %v", err)
	}
	return signer
}

func testAddr(t *testing.T) net.Addr {
	t.Helper()
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:22")
	if err != nil {
		t.Fatalf("resolving addr: %v", err)
	}
	return addr
}

// knownHostsIn returns a path in a fresh temp dir, without creating the file.
func knownHostsIn(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "known_hosts")
}

func TestHostKeyOffAcceptsAnything(t *testing.T) {
	cb, err := hostKeyCallback(config.HostKeyOff, knownHostsIn(t))
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	if err := cb("localhost:22", testAddr(t), testKey(t)); err != nil {
		t.Errorf("off refused a key: %v", err)
	}
}

func TestHostKeyAcceptNewRecordsThenPins(t *testing.T) {
	path := knownHostsIn(t)
	cb, err := hostKeyCallback(config.HostKeyAcceptNew, path)
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}

	key := testKey(t)
	if err := cb("localhost:10022", testAddr(t), key); err != nil {
		t.Fatalf("accept-new refused an unknown host: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading known_hosts: %v", err)
	}
	if !strings.Contains(string(body), "[localhost]:10022") {
		t.Errorf("known_hosts does not contain the host: %q", body)
	}
	if lines := strings.Count(strings.TrimSpace(string(body)), "\n") + 1; lines != 1 {
		t.Errorf("recorded %d lines, want 1", lines)
	}

	// The same key again must be accepted without appending a second line.
	if err := cb("localhost:10022", testAddr(t), key); err != nil {
		t.Errorf("accept-new refused a key it had just recorded: %v", err)
	}

	// A *different* key for a host we know is the interception case.
	if err := cb("localhost:10022", testAddr(t), testKey(t)); err == nil {
		t.Error("accept-new accepted a changed host key, want refusal")
	}
}

func TestHostKeyStrictRefusesUnknownHost(t *testing.T) {
	cb, err := hostKeyCallback(config.HostKeyStrict, knownHostsIn(t))
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	if err := cb("localhost:10022", testAddr(t), testKey(t)); err == nil {
		t.Error("strict accepted an unknown host, want refusal")
	}
}

// Several tunnels routinely forward through one SSH server — the two
// playground tunnels both use localhost:13022 — and they dial concurrently.
// Recording the key twice is what actually happened before knownHostsMu was
// made package-level.
func TestAcceptNewRecordsOnceUnderConcurrency(t *testing.T) {
	path := knownHostsIn(t)
	key := testKey(t)
	addr := testAddr(t)

	// A separate callback per tunnel, exactly as serveSSH builds them.
	const tunnels = 8
	callbacks := make([]ssh.HostKeyCallback, tunnels)
	for i := range callbacks {
		cb, err := hostKeyCallback(config.HostKeyAcceptNew, path)
		if err != nil {
			t.Fatalf("hostKeyCallback: %v", err)
		}
		callbacks[i] = cb
	}

	var wg sync.WaitGroup
	errs := make([]error, tunnels)
	for i, cb := range callbacks {
		wg.Go(func() { errs[i] = cb("localhost:13022", addr, key) })
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("callback %d refused the key: %v", i, err)
		}
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading known_hosts: %v", err)
	}
	if got := strings.Count(strings.TrimSpace(string(body)), "\n") + 1; got != 1 {
		t.Errorf("recorded %d lines for one host key, want 1:\n%s", got, body)
	}
}

func TestEnsureKnownHostsFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "known_hosts")
	if err := ensureKnownHostsFile(path); err != nil {
		t.Fatalf("ensureKnownHostsFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// known_hosts is not secret, but it is written into ~/.ssh and should not
	// be looser than everything else there.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
	// Existing files must be left exactly as they are.
	if err := os.WriteFile(path, []byte("existing\n"), 0o600); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if err := ensureKnownHostsFile(path); err != nil {
		t.Fatalf("ensureKnownHostsFile on an existing file: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(body) != "existing\n" {
		t.Errorf("existing file was modified: %q", body)
	}
}

func TestSSHLoginDefaultsToLocalUser(t *testing.T) {
	got, err := sshLogin(config.Tunnel{Name: "t", User: "configured"})
	if err != nil {
		t.Fatalf("sshLogin: %v", err)
	}
	if got != "configured" {
		t.Errorf("sshLogin = %q, want %q", got, "configured")
	}

	got, err = sshLogin(config.Tunnel{Name: "t"})
	if err != nil {
		t.Fatalf("sshLogin with no user: %v", err)
	}
	if got == "" {
		t.Error("sshLogin returned an empty username")
	}
}

func TestHostKeyCallbackRejectsUnknownMode(t *testing.T) {
	// config.Validate rejects this first, but the callback must not silently
	// fall through to something permissive if it ever gets here.
	cb, err := hostKeyCallback("banana", knownHostsIn(t))
	if err != nil {
		return // refusing to build one is a fine answer
	}
	if err := cb("localhost:22", testAddr(t), testKey(t)); err == nil {
		t.Error("an unknown mode accepted a key, want refusal")
	}
}
