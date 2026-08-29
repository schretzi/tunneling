// Package health records what the daemon observes about each tunnel's
// traffic, and publishes it where `status` can read it.
//
// This exists because a separate `status` process cannot tell a working
// tunnel from a broken one. Probing the local port only proves a listener is
// bound, which stays true for a tunnel whose destination has been deleted —
// that is exactly how a tunnel to a removed GCP project reported OPEN for
// hours while failing every single connection. Only the daemon, which does
// the forwarding, knows whether anything actually got through.
package health

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Outcome is what one forwarded connection tells us about a tunnel.
type Outcome int

const (
	// OutcomeNeutral is a connection that proves nothing: the client
	// connected and went away without sending anything, so the far end was
	// never asked. `status`'s own port probe looks exactly like this, and
	// must not be mistaken for either health or breakage.
	OutcomeNeutral Outcome = iota
	// OutcomeSuccess is a connection that received data from the far end.
	OutcomeSuccess
	// OutcomeFailure is a connection that could not be established, or one
	// that sent data and received nothing back.
	OutcomeFailure
)

// Tunnel is the accumulated record for one tunnel.
type Tunnel struct {
	// LastSuccess and LastFailure are the times of the most recent
	// success/failure. Which one is later decides the reported state.
	LastSuccess time.Time `json:"lastSuccess,omitzero"`
	LastFailure time.Time `json:"lastFailure,omitzero"`
	// LastError is the message behind LastFailure, for display.
	LastError string `json:"lastError,omitempty"`
	// Failures counts consecutive failures since the last success, so a
	// tunnel that recovered does not keep showing a scary total.
	Failures int64 `json:"failures"`
	// Successes and BytesReceived are lifetime totals for this daemon run.
	Successes     int64 `json:"successes"`
	BytesReceived int64 `json:"bytesReceived"`
}

// Snapshot is the whole published file.
type Snapshot struct {
	// PID and StartedAt identify the daemon run these records belong to, so
	// a reader can tell live data from a file left behind by a dead process.
	PID       int               `json:"pid"`
	StartedAt time.Time         `json:"startedAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
	Tunnels   map[string]Tunnel `json:"tunnels"`
}

// Recorder accumulates outcomes and publishes them. Safe for concurrent use:
// every forwarded connection reports through it.
type Recorder struct {
	path string

	mu      sync.Mutex
	snap    Snapshot
	dirty   bool
	flushed time.Time
}

// New returns a Recorder for the given tunnel names, publishing to path.
// Names are seeded so a tunnel that has never carried a connection still
// appears, reported as idle rather than missing.
func New(path string, names []string) *Recorder {
	tunnels := make(map[string]Tunnel, len(names))
	for _, n := range names {
		tunnels[n] = Tunnel{}
	}
	return &Recorder{
		path: path,
		snap: Snapshot{
			PID:       os.Getpid(),
			StartedAt: time.Now(),
			Tunnels:   tunnels,
		},
	}
}

// Record folds one connection's outcome into the tunnel's record.
// bytesReceived is what came back from the far end; errMsg describes a
// failure and is ignored otherwise.
func (r *Recorder) Record(name string, outcome Outcome, bytesReceived int64, errMsg string) {
	// A nil Recorder records nothing, so callers that have no state file to
	// write to (tests, a future one-shot mode) need no branching.
	if r == nil || outcome == OutcomeNeutral {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	t := r.snap.Tunnels[name]
	now := time.Now()
	switch outcome {
	case OutcomeSuccess:
		t.LastSuccess = now
		t.Successes++
		t.BytesReceived += bytesReceived
		// Consecutive, not lifetime: a tunnel that came back should stop
		// looking broken.
		t.Failures = 0
		t.LastError = ""
	case OutcomeFailure:
		t.LastFailure = now
		t.Failures++
		t.LastError = errMsg
	case OutcomeNeutral:
		return
	}
	r.snap.Tunnels[name] = t
	r.dirty = true
}

// Seed writes the initial snapshot, before any connection has been made.
//
// Without it a freshly started daemon leaves no file at all, which is
// indistinguishable from "no daemon has ever run" — and the difference
// matters: the first means every tunnel is idle, the second means `status`
// has nothing to say.
func (r *Recorder) Seed() error {
	// A daemon killed between CreateTemp and rename leaves its temp file
	// behind, and nothing else would ever remove it. Startup is the safe
	// moment to sweep: only the daemon writes here, and it has no write of
	// its own in flight yet.
	sweepTempFiles(filepath.Dir(r.path))

	r.mu.Lock()
	r.dirty = true
	r.mu.Unlock()
	return r.Flush()
}

// sweepTempFiles removes leftovers from interrupted writes. Failures are
// ignored: this is housekeeping, and being unable to tidy up is not a reason
// to refuse to start.
func sweepTempFiles(dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, tempPattern))
	if err != nil {
		return
	}
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

// tempPattern is the name interrupted writes leave behind, and what Seed
// sweeps. The leading dot keeps them out of a casual `ls`.
const tempPattern = ".health-*.json"

// flushInterval bounds how often the file is rewritten under load. A busy
// tunnel would otherwise rewrite it per connection for no added truth.
const flushInterval = 2 * time.Second

// Flush writes the snapshot if anything changed since the last write.
func (r *Recorder) Flush() error {
	r.mu.Lock()
	if !r.dirty {
		r.mu.Unlock()
		return nil
	}
	r.snap.UpdatedAt = time.Now()
	r.dirty = false
	r.flushed = time.Now()
	data, err := json.MarshalIndent(r.snap, "", "  ")
	r.mu.Unlock()
	if err != nil {
		return fmt.Errorf("encoding health snapshot: %w", err)
	}
	return writeAtomic(r.path, data)
}

// Publish keeps the file up to date until ctx-driven cancellation, via the
// stop channel. It is the daemon's only writer.
func (r *Recorder) Publish(done <-chan struct{}) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = r.Flush()
		case <-done:
			// A final write so the last failure before shutdown is not lost.
			_ = r.Flush()
			return
		}
	}
}

// Path returns the file the Recorder publishes to.
func (r *Recorder) Path() string { return r.path }

// writeAtomic replaces path in one step, so a reader never sees a half-written
// file.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating state directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return fmt.Errorf("creating temp state file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmpName, err)
	}
	// The state names internal hosts and error strings; nothing else needs
	// to read it.
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("setting mode on %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// Load reads a published snapshot. A missing file is not an error: it means
// no daemon has run yet, which callers report as unknown rather than broken.
func Load(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the configured state path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &snap, nil
}

// Live reports whether the snapshot belongs to a daemon that is still
// running. A file left behind by a dead daemon describes the past, and
// treating it as current is how a stopped tunnel would keep reporting OK.
func (s *Snapshot) Live() bool {
	if s == nil || s.PID <= 0 {
		return false
	}
	proc, err := os.FindProcess(s.PID)
	if err != nil {
		return false
	}
	// On Unix FindProcess always succeeds, so signal 0 is the actual test: it
	// checks whether the process exists without delivering anything.
	//
	// Not Signal(nil): that fails os's type assertion to syscall.Signal and
	// so always reports an error, which would make every snapshot look dead.
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM means the process exists but belongs to someone else. That should
	// not happen — the daemon runs as the user reading this — but "exists"
	// is the honest answer to the question being asked.
	return errors.Is(err, syscall.EPERM)
}
