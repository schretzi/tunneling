package tunnel

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schretzi/tunneling/internal/config"
)

// quietLogs silences the standard logger for the duration of a test, so the
// package's own log output does not interleave with `go test` results.
func quietLogs(t *testing.T) {
	t.Helper()
	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(prev) })
}

// A nil *health.Recorder is deliberately supported: these tests exercise the
// tunnel lifecycle, not health publishing, and Record tolerates a nil
// receiver.
func TestRunRejectsEmptySelection(t *testing.T) {
	quietLogs(t)
	err := Run(t.Context(), &config.Config{}, nil, nil)
	if err == nil {
		t.Fatal("Run succeeded with no tunnels, want error")
	}
}

func TestRunRejectsUnknownKind(t *testing.T) {
	quietLogs(t)
	cfg := &config.Config{BindAddress: "127.0.0.1"}
	// config.Validate would normally reject this; serve must still not
	// silently succeed if it ever gets through.
	err := Run(t.Context(), cfg, []config.Tunnel{{Name: "x", Kind: "telnet"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("Run error = %v, want it to mention an unknown kind", err)
	}
}

// A tunnel that cannot bind its port must fail the whole Run, not be skipped:
// a silently missing tunnel is the failure mode this design exists to avoid.
func TestRunFailsOnPortInUse(t *testing.T) {
	quietLogs(t)

	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	defer func() { _ = blocker.Close() }()

	_, port, err := net.SplitHostPort(blocker.Addr().String())
	if err != nil {
		t.Fatalf("splitting %s: %v", blocker.Addr(), err)
	}

	// google.DefaultTokenSource is consulted before the listener is bound, so
	// point it at a credentials file that exists and parses. It is never used
	// to reach the network here.
	writeFakeADC(t)

	cfg := &config.Config{BindAddress: "127.0.0.1"}
	tun := config.Tunnel{
		Name: "busy", Kind: config.KindGCP,
		Project: "p", Zone: "z", Nic: "nic0",
		RemoteHost: "instance", RemotePort: "22",
		LocalPort: config.Port(port),
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	if err := Run(ctx, cfg, []config.Tunnel{tun}, nil); err == nil {
		t.Fatal("Run succeeded with the port already bound, want error")
	} else if !strings.Contains(err.Error(), "busy") {
		t.Errorf("Run error = %v, want it to name the tunnel", err)
	}
}

// Cancelling the context must bring Run down cleanly rather than hanging:
// this is what makes SIGTERM (and so `service stop`) work.
func TestRunStopsOnContextCancel(t *testing.T) {
	quietLogs(t)
	writeFakeADC(t)

	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("finding a free port: %v", err)
	}
	_, port, err := net.SplitHostPort(free.Addr().String())
	if err != nil {
		t.Fatalf("splitting %s: %v", free.Addr(), err)
	}
	_ = free.Close()

	cfg := &config.Config{BindAddress: "127.0.0.1"}
	tun := config.Tunnel{
		Name: "idle", Kind: config.KindGCP,
		Project: "p", Zone: "z", Nic: "nic0",
		RemoteHost: "instance", RemotePort: "22",
		LocalPort: config.Port(port),
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, []config.Tunnel{tun}, nil) }()

	// Wait for the listener to actually be up before cancelling, so the test
	// exercises the shutdown path rather than a startup failure.
	waitForListener(t, net.JoinHostPort("127.0.0.1", port))
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run after cancel = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10s of context cancellation")
	}
}

func waitForListener(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("nothing listening on %s after 5s", addr)
}

// writeFakeADC points Google's application-default-credentials lookup at a
// syntactically valid file, so resolving the token source succeeds without
// depending on whatever the developer happens to be logged into.
func writeFakeADC(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "adc.json")
	const creds = `{
  "type": "authorized_user",
  "client_id": "test-client-id",
  "client_secret": "test-client-secret",
  "refresh_token": "test-refresh-token"
}`
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatalf("writing fake credentials: %v", err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
}

func TestCheckSSHAgentWithoutSocket(t *testing.T) {
	quietLogs(t)
	t.Setenv("SSH_AUTH_SOCK", "")

	err := checkSSHAgent(t.Context())
	if err == nil {
		t.Fatal("checkSSHAgent succeeded with SSH_AUTH_SOCK unset, want error")
	}
	if !strings.Contains(err.Error(), "SSH_AUTH_SOCK") {
		t.Errorf("error = %v, want it to name SSH_AUTH_SOCK", err)
	}
}

func TestCheckSSHAgentUnreachableSocket(t *testing.T) {
	quietLogs(t)
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(t.TempDir(), "absent.sock"))

	err := checkSSHAgent(t.Context())
	if err == nil {
		t.Fatal("checkSSHAgent succeeded with a missing socket, want error")
	}
	if !strings.Contains(err.Error(), "connecting to ssh-agent") {
		t.Errorf("error = %v, want it to report a connection failure", err)
	}
}
