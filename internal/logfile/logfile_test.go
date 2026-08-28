package logfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// forceNextCheck backdates lastCheck so the next Write re-stats instead of
// being skipped by checkInterval. Real rotations are hours apart; tests are
// not, and sleeping a second per case is not worth it.
func forceNextCheck(w *Writer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastCheck = time.Now().Add(-2 * checkInterval)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

func TestWriteAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Close()

	for _, line := range []string{"one\n", "two\n"} {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if got, want := readFile(t, path), "one\ntwo\n"; got != want {
		t.Errorf("log contents = %q, want %q", got, want)
	}
}

// TestReopensAfterRename is the case the whole package exists for: newsyslog
// renames the log, and writes must land in a fresh file at the original path
// rather than following the rename into the archive.
func TestReopensAfterRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	rotated := filepath.Join(dir, "test.log.0")

	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Close()

	if _, err := w.Write([]byte("before\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := os.Rename(path, rotated); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	forceNextCheck(w)
	if _, err := w.Write([]byte("after\n")); err != nil {
		t.Fatalf("Write after rotation: %v", err)
	}

	if got := readFile(t, rotated); got != "before\n" {
		t.Errorf("archive = %q, want %q — post-rotation writes leaked into it", got, "before\n")
	}
	if got := readFile(t, path); got != "after\n" {
		t.Errorf("new log = %q, want %q", got, "after\n")
	}
}

// TestReopensAfterUnlink covers newsyslog's other outcome: the log is removed
// (count exhausted) with nothing put back in its place.
func TestReopensAfterUnlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Close()

	if _, err := w.Write([]byte("before\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	forceNextCheck(w)
	if _, err := w.Write([]byte("after\n")); err != nil {
		t.Fatalf("Write after unlink: %v", err)
	}
	if got := readFile(t, path); got != "after\n" {
		t.Errorf("recreated log = %q, want %q", got, "after\n")
	}
}

// TestCheckIntervalThrottles pins the throttle down: without an elapsed
// checkInterval a rotation is not yet noticed, which is the tradeoff that
// keeps Write down to one stat per second rather than one per call.
func TestCheckIntervalThrottles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Close()

	if err := os.Rename(path, filepath.Join(dir, "test.log.0")); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	// No forceNextCheck: the interval has not elapsed since Open.
	if _, err := w.Write([]byte("straggler\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("log was reopened inside checkInterval; stat err = %v", err)
	}
}

func TestCreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "test.log")
	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Close()

	if _, err := w.Write([]byte("hi\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := readFile(t, path); got != "hi\n" {
		t.Errorf("log = %q, want %q", got, "hi\n")
	}
}

func TestWriteAfterCloseFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close = %v, want nil (Close must be idempotent)", err)
	}
	if _, err := w.Write([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Errorf("Write after Close = %v, want ErrClosed", err)
	}
}

func TestConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer w.Close()

	const writers, perWriter = 8, 50
	done := make(chan struct{})
	for range writers {
		go func() {
			defer func() { done <- struct{}{} }()
			for range perWriter {
				if _, err := w.Write([]byte("line\n")); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}()
	}
	for range writers {
		<-done
	}

	if got, want := strings.Count(readFile(t, path), "line\n"), writers*perWriter; got != want {
		t.Errorf("wrote %d lines, want %d", got, want)
	}
}
