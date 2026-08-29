package tunnel

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"

	"github.com/schretzi/tunneling/internal/config"
	"github.com/schretzi/tunneling/internal/health"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// sshDialTimeout bounds the TCP connect and handshake with the SSH server.
// Without it a jump host that accepts the connection and then goes silent —
// a half-dropped VPN, a wedged IAP tunnel — hangs the forward indefinitely.
const sshDialTimeout = 15 * time.Second

// serveSSH runs a local port forward through an SSH server, authenticating
// with the keys held by the running ssh-agent.
//
// One multiplexed SSH connection per tunnel, the way `ssh -L` works: each
// forwarded connection opens a channel on it instead of performing a fresh
// TCP connect and SSH handshake. That is both faster and the fix for a leak —
// the library this replaced (elliotchance/sshtunnel) dialed a new ssh.Client
// per forwarded connection and closed none of them until the tunnel shut
// down, so every kubectl call permanently leaked an SSH client and, through
// it, an IAP connection to Google.
func serveSSH(ctx context.Context, cfg *config.Config, t config.Tunnel, rec *health.Recorder) error {
	// Resolve everything that can fail on bad configuration now, so a typo
	// is a startup error rather than a surprise on the first connection.
	login, err := sshLogin(t)
	if err != nil {
		return err
	}
	hostKey, err := hostKeyCallback(cfg.HostKeyChecking, cfg.KnownHostsPath)
	if err != nil {
		return err
	}

	dialer := &sshDialer{
		server:  t.SSHAddr(),
		newConf: sshClientConfig(login, hostKey),
	}
	defer dialer.close()

	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", cfg.LocalAddr(t))
	if err != nil {
		return err
	}
	// Unblocks the Accept below on shutdown; Accept then returns
	// net.ErrClosed, which the loop treats as a clean stop.
	context.AfterFunc(ctx, func() { _ = listener.Close() })

	log.Printf("tunnel %s: SSH listening on %s -> %s via %s",
		t.Name, listener.Addr(), t.Endpoint(), t.SSHServer())

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accepting on %s: %w", listener.Addr(), err)
		}
		go forwardSSH(ctx, t, dialer, conn, rec)
	}
}

// forwardSSH carries one accepted connection to the tunnel's destination over
// the tunnel's shared SSH connection.
//
// Errors here are per-connection and never fatal: an unreachable jump host
// should cost you that connection, not every tunnel in the process.
func forwardSSH(ctx context.Context, t config.Tunnel, dialer *sshDialer, local net.Conn, rec *health.Recorder) {
	defer func() { _ = local.Close() }()

	remote, err := dialThroughSSH(ctx, t, dialer)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("tunnel %s: %v", t.Name, err)
			rec.Record(t.Name, health.OutcomeFailure, 0, err.Error())
		}
		return
	}
	defer func() { _ = remote.Close() }()

	log.Printf("tunnel %s: connected %s -> %s", t.Name, local.RemoteAddr(), t.Endpoint())
	// Report health as soon as the far end answers, not only when the
	// connection ends: a multiplexed session can stay open for hours.
	firstReply := func() { rec.Record(t.Name, health.OutcomeSuccess, 0, "") }
	moved := pipe(ctx, t.Name, local, remote, firstReply)
	outcome, reason := moved.outcome()
	rec.Record(t.Name, outcome, moved.FromRemote, reason)
	log.Printf("tunnel %s: disconnected %s (%d bytes)", t.Name, local.RemoteAddr(), moved.total())
}

// dialThroughSSH opens a channel to the tunnel's destination, retrying once on
// a freshly dialed connection.
//
// The retry matters because the cached SSH connection can be dead before
// Wait has noticed — a rebooted jump host, a dropped VPN, an IAP tunnel that
// went away underneath it. Without it, the first connection after any such
// break fails even though reconnecting would have worked.
func dialThroughSSH(ctx context.Context, t config.Tunnel, dialer *sshDialer) (net.Conn, error) {
	var lastErr error
	for attempt := range 2 {
		client, err := dialer.get(ctx)
		if err != nil {
			// A failed handshake is not something a retry fixes: the server
			// is unreachable, or it rejected our keys.
			return nil, err
		}
		remote, err := client.DialContext(ctx, "tcp", t.Endpoint())
		if err == nil {
			return remote, nil
		}
		lastErr = err
		if attempt == 0 && ctx.Err() == nil {
			dialer.invalidate(client)
		}
	}
	return nil, fmt.Errorf("dialing %s through %s: %w", t.Endpoint(), t.SSHAddr(), lastErr)
}

// sshDialer owns the single SSH connection a tunnel multiplexes its forwards
// over, dialing it lazily and re-dialing after it drops.
//
// Lazily on purpose: an ssh tunnel commonly points at the local port of a gcp
// tunnel in this same process, so the server it needs may not be listening
// yet when the tunnel starts. Dialing on first use sidesteps that ordering
// entirely.
type sshDialer struct {
	server  string
	newConf func(context.Context) (*ssh.ClientConfig, net.Conn, error)

	mu        sync.Mutex
	client    *ssh.Client
	agentConn net.Conn
}

// get returns the live SSH connection, dialing one if there is none.
func (d *sshDialer) get(ctx context.Context) (*ssh.Client, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client != nil {
		return d.client, nil
	}

	// The agent connection is built per SSH connection and lives as long as
	// it does: its signers are used during the handshake and again on any
	// re-authentication.
	config, agentConn, err := d.newConf(ctx)
	if err != nil {
		return nil, err
	}

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", d.server)
	if err != nil {
		_ = agentConn.Close()
		return nil, fmt.Errorf("dialing ssh server %s: %w", d.server, err)
	}
	// NewClientConn rather than ssh.Dial: it takes the net.Conn we dialed
	// with a context, so shutdown interrupts a hanging connect.
	c, chans, reqs, err := ssh.NewClientConn(conn, d.server, config)
	if err != nil {
		_ = conn.Close()
		_ = agentConn.Close()
		return nil, fmt.Errorf("ssh handshake with %s: %w", d.server, err)
	}
	client := ssh.NewClient(c, chans, reqs)
	d.client, d.agentConn = client, agentConn

	// Forget the connection as soon as it ends, so the next forward dials a
	// fresh one rather than failing on a dead handle.
	go func() {
		_ = client.Wait()
		d.mu.Lock()
		if d.client == client {
			d.client, d.agentConn = nil, nil
			_ = agentConn.Close()
		}
		d.mu.Unlock()
	}()
	return client, nil
}

// invalidate drops client from the cache and closes it, if it is still the
// current one.
func (d *sshDialer) invalidate(client *ssh.Client) {
	d.mu.Lock()
	if d.client == client {
		d.client = nil
		if d.agentConn != nil {
			_ = d.agentConn.Close()
			d.agentConn = nil
		}
	}
	d.mu.Unlock()
	_ = client.Close()
}

// close tears down the tunnel's SSH connection.
func (d *sshDialer) close() {
	d.mu.Lock()
	client, agentConn := d.client, d.agentConn
	d.client, d.agentConn = nil, nil
	d.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
	if agentConn != nil {
		_ = agentConn.Close()
	}
}

// sshClientConfig returns the function the dialer calls to build a client
// configuration for each SSH connection, along with the agent connection that
// configuration borrows its signers from.
func sshClientConfig(login string, hostKey ssh.HostKeyCallback) func(context.Context) (*ssh.ClientConfig, net.Conn, error) {
	return func(ctx context.Context) (*ssh.ClientConfig, net.Conn, error) {
		agentConn, auth, err := dialAgent(ctx)
		if err != nil {
			return nil, nil, err
		}
		return &ssh.ClientConfig{
			User:            login,
			Auth:            []ssh.AuthMethod{auth},
			HostKeyCallback: hostKey,
			Timeout:         sshDialTimeout,
		}, agentConn, nil
	}
}

// sshLogin is the tunnel's SSH username, defaulting to the local one as
// ssh(1) does.
func sshLogin(t config.Tunnel) (string, error) {
	if t.User != "" {
		return t.User, nil
	}
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("tunnel %s: no user configured and cannot determine the local one: %w", t.Name, err)
	}
	return u.Username, nil
}

// dialAgent opens a connection to the running ssh-agent.
//
// One per SSH connection, owned and closed by the sshDialer. The signers it
// returns sign *during* the handshake, so the connection has to outlive the
// call that produced them — and reconnecting to the agent on each handshake
// picks up keys added with ssh-add since the daemon started.
func dialAgent(ctx context.Context) (net.Conn, ssh.AuthMethod, error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, nil, errors.New(`SSH_AUTH_SOCK is not set, so no ssh-agent is reachable; ` +
			`tunnels of kind "ssh" need one`)
	}
	// #nosec G704 -- SSH_AUTH_SOCK is the address of the user's own agent;
	// connecting to whatever it names is the entire point.
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to ssh-agent at %s: %w", socket, err)
	}
	return conn, ssh.PublicKeysCallback(agent.NewClient(conn).Signers), nil
}

// hostKeyCallback builds the host key verification callback for mode.
//
// An unrecognized mode is an error rather than a fallback. config.Validate
// rejects one first, so this is unreachable — but the cost of being wrong
// about that is silently accepting host keys, and a security check must fail
// closed even on a path that "cannot happen".
func hostKeyCallback(mode, knownHostsPath string) (ssh.HostKeyCallback, error) {
	switch mode {
	case config.HostKeyOff:
		// #nosec G106 -- explicitly requested with `hostKeyChecking: off`;
		// the default is accept-new and this is the documented escape hatch.
		return ssh.InsecureIgnoreHostKey(), nil
	case config.HostKeyAcceptNew, config.HostKeyStrict:
	default:
		return nil, fmt.Errorf("unknown hostKeyChecking mode %q", mode)
	}

	path, err := config.ExpandPath(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("resolving knownHostsPath %s: %w", knownHostsPath, err)
	}
	// knownhosts.New fails outright on a missing file, which would make a
	// fresh machine unable to connect at all under accept-new.
	if err := ensureKnownHostsFile(path); err != nil {
		return nil, err
	}
	verify, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("reading known hosts %s: %w", path, err)
	}
	if mode == config.HostKeyStrict {
		return verify, nil
	}

	// accept-new: record hosts we have never seen, but never accept a
	// *different* key for a host we have.
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := verify(hostname, remote, key)
		if err == nil {
			return nil
		}
		if !isUnknownHost(err) {
			// A KeyError with a non-empty Want is a known host presenting a
			// different key, which is indistinguishable from interception.
			// Anything else is a genuine failure. Either way, refuse.
			return err
		}
		return recordHostKey(path, hostname, remote, key)
	}, nil
}

// isUnknownHost reports whether err means "no entry for this host" rather
// than "this host's key changed".
func isUnknownHost(err error) bool {
	var keyErr *knownhosts.KeyError
	return errors.As(err, &keyErr) && len(keyErr.Want) == 0
}

// knownHostsMu serializes appends to known_hosts.
//
// Package-level, not per-callback: each tunnel builds its own callback, and
// several tunnels routinely share one SSH server — the two playground tunnels
// both forward through localhost:13022 — so a per-callback lock serializes
// nothing and both write the same key.
var knownHostsMu sync.Mutex

// recordHostKey appends key for hostname, unless another tunnel recorded it
// first.
func recordHostKey(path, hostname string, remote net.Addr, key ssh.PublicKey) error {
	knownHostsMu.Lock()
	defer knownHostsMu.Unlock()

	// Re-read the file rather than reusing the caller's callback: that one
	// parsed known_hosts when the tunnel started and cannot see a line a
	// concurrent tunnel has since appended.
	if fresh, err := knownhosts.New(path); err == nil {
		switch err := fresh(hostname, remote, key); {
		case err == nil:
			return nil // already recorded, by us or by ssh(1)
		case !isUnknownHost(err):
			return err
		}
	}

	if err := appendKnownHost(path, hostname, key); err != nil {
		return err
	}
	log.Printf("ssh: recorded new host key for %s (%s) in %s", hostname, key.Type(), path)
	return nil
}

// ensureKnownHostsFile creates an empty known_hosts (and its directory) when
// there is none.
func ensureKnownHostsFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking known hosts %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- the configured known_hosts path
	if err != nil {
		return fmt.Errorf("creating known hosts %s: %w", path, err)
	}
	return f.Close()
}

// appendKnownHost adds one host key line, in the format ssh(1) writes.
func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- the configured known_hosts path
	if err != nil {
		return fmt.Errorf("opening known hosts %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("appending to known hosts %s: %w", path, err)
	}
	return nil
}

// checkSSHAgent verifies that an ssh-agent is reachable and actually holds
// keys.
//
// Worth doing up front: an agent with no keys otherwise fails later as an
// opaque authentication error on the first connection rather than at startup.
func checkSSHAgent(ctx context.Context) error {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return errors.New(`SSH_AUTH_SOCK is not set, so no ssh-agent is reachable; ` +
			`tunnels of kind "ssh" need one`)
	}

	// #nosec G704 -- SSH_AUTH_SOCK is the address of the user's own agent;
	// connecting to whatever it names is the entire point.
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return fmt.Errorf("connecting to ssh-agent at %s: %w", socket, err)
	}
	defer func() { _ = conn.Close() }()

	keys, err := agent.NewClient(conn).List()
	if err != nil {
		return fmt.Errorf("listing ssh-agent identities: %w", err)
	}
	if len(keys) == 0 {
		return fmt.Errorf("ssh-agent at %s is running but holds no identities; "+
			"add one with `ssh-add`", socket)
	}

	log.Printf("ssh-agent: %d identity/identities available", len(keys))
	return nil
}
