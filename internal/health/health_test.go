package health

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func statePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state", "health.json")
}

func TestSeedPublishesIdleTunnels(t *testing.T) {
	path := statePath(t)
	rec := New(path, []string{"jump-dev", "k8s-dev"})
	if err := rec.Seed(); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap == nil {
		t.Fatal("Load returned no snapshot after Seed")
	}
	if len(snap.Tunnels) != 2 {
		t.Errorf("got %d tunnels, want 2", len(snap.Tunnels))
	}
	// A seeded tunnel must read as "no evidence", not as missing.
	if entry, ok := snap.Tunnels["jump-dev"]; !ok {
		t.Error("jump-dev missing from the seeded snapshot")
	} else if !entry.LastSuccess.IsZero() || !entry.LastFailure.IsZero() {
		t.Errorf("seeded tunnel has timestamps: %+v", entry)
	}
	if snap.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", snap.PID, os.Getpid())
	}
	if !snap.Live() {
		t.Error("Live() = false for a snapshot from this very process")
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	snap, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load on a missing file: %v", err)
	}
	if snap != nil {
		t.Errorf("Load returned %+v, want nil", snap)
	}
	// A nil snapshot must not be reported as live.
	if snap.Live() {
		t.Error("nil snapshot reported as live")
	}
}

func TestDeadPIDIsNotLive(t *testing.T) {
	// PID 0 is never a real process, and a negative one is nonsense; both
	// stand in for "this file was left by a daemon that is gone".
	for _, pid := range []int{0, -1} {
		snap := &Snapshot{PID: pid}
		if snap.Live() {
			t.Errorf("PID %d reported as live", pid)
		}
	}
}

func TestRecordOutcomes(t *testing.T) {
	rec := New(statePath(t), []string{"t"})

	// Neutral proves nothing and must not touch the record — this is what
	// `status`'s own port probe looks like.
	rec.Record("t", OutcomeNeutral, 0, "")
	if got := rec.snap.Tunnels["t"]; got.Successes != 0 || got.Failures != 0 {
		t.Errorf("a neutral outcome changed the record: %+v", got)
	}

	rec.Record("t", OutcomeFailure, 0, "code 4033")
	rec.Record("t", OutcomeFailure, 0, "code 4033")
	got := rec.snap.Tunnels["t"]
	if got.Failures != 2 {
		t.Errorf("Failures = %d, want 2", got.Failures)
	}
	if got.LastError != "code 4033" {
		t.Errorf("LastError = %q, want %q", got.LastError, "code 4033")
	}

	// A success clears the consecutive failure count: a tunnel that came
	// back should stop looking broken.
	rec.Record("t", OutcomeSuccess, 1695, "")
	got = rec.snap.Tunnels["t"]
	if got.Failures != 0 {
		t.Errorf("Failures = %d after a success, want 0", got.Failures)
	}
	if got.LastError != "" {
		t.Errorf("LastError = %q after a success, want empty", got.LastError)
	}
	if got.BytesReceived != 1695 {
		t.Errorf("BytesReceived = %d, want 1695", got.BytesReceived)
	}
	if !got.LastSuccess.After(got.LastFailure) {
		t.Error("LastSuccess is not after LastFailure, so status would report FAILING")
	}
}

func TestNilRecorderIsANoOp(_ *testing.T) {
	var rec *Recorder
	// Must not panic: forwards call this unconditionally.
	rec.Record("t", OutcomeFailure, 0, "boom")
}

func TestRecordIsConcurrencySafe(t *testing.T) {
	rec := New(statePath(t), []string{"t"})

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { rec.Record("t", OutcomeSuccess, 10, "") })
	}
	wg.Wait()

	if got := rec.snap.Tunnels["t"]; got.Successes != 50 || got.BytesReceived != 500 {
		t.Errorf("got %d successes / %d bytes, want 50 / 500", got.Successes, got.BytesReceived)
	}
}

// A reader must never see a partly written file, so the write goes through a
// temp file and a rename.
func TestFlushIsAtomicAndPrivate(t *testing.T) {
	path := statePath(t)
	rec := New(path, []string{"t"})
	rec.Record("t", OutcomeSuccess, 1, "")
	if err := rec.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// The file names internal hosts and error strings.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}

	// No temp files left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("state dir holds %v, want only health.json", names)
	}

	// And it is valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("published file is not valid JSON: %v", err)
	}
}

func TestFlushSkipsWhenNothingChanged(t *testing.T) {
	path := statePath(t)
	rec := New(path, []string{"t"})
	rec.Record("t", OutcomeSuccess, 1, "")
	if err := rec.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	first, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	if err := rec.Flush(); err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	second, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !first.ModTime().Equal(second.ModTime()) {
		t.Error("Flush rewrote the file with nothing to report")
	}
}

// A daemon killed between CreateTemp and rename leaves a temp file behind,
// and nothing else would ever remove it.
func TestSeedSweepsLeftoverTempFiles(t *testing.T) {
	path := statePath(t)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := filepath.Join(dir, ".health-999999.json")
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatalf("seeding a leftover: %v", err)
	}

	if err := New(path, []string{"t"}).Seed(); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("leftover temp file survived Seed (stat err = %v)", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("Seed did not write the state file: %v", err)
	}
}
