// Package logfile provides an append-mode log writer that survives an
// external rotation.
//
// Rotation is newsyslog's job (see MacbookSetup/CONVENTIONS.md). newsyslog
// rotates by *renaming* the log — it has no copy-truncate mode — and a
// long-running process that simply holds an fd keeps writing into the
// renamed, and then bzip2-compressed, inode. The new log file stays empty
// forever. launchd does not reopen StandardOutPath/StandardErrorPath either,
// so nothing rescues this from the outside.
//
// Writer closes that hole: before writing it checks (at most once a second)
// whether the file at its path is still the one it holds open, and reopens
// when it is not.
//
// This file is duplicated verbatim in KerberosKeepAlive, OauthMailToken,
// macswitcher and tunneling. They are four separate modules with no shared
// dependency; keep the copies in sync by hand.
package logfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// checkInterval bounds how often Write stats the log path. newsyslog runs
// hourly, so a one-second window is far tighter than it needs to be while
// still costing only one stat per second of active logging.
const checkInterval = time.Second

// ErrClosed is returned by Write after Close.
var ErrClosed = errors.New("logfile: writer is closed")

// Writer is an io.WriteCloser that reopens its file when an external rotation
// moves it away. It is safe for concurrent use.
type Writer struct {
	path string

	mu   sync.Mutex
	file *os.File
	// info identifies the file we currently hold open, for comparison against
	// whatever is at path later. os.FileInfo rather than a (dev, ino) pair:
	// syscall.Stat_t.Dev is int32 on darwin and uint64 on Linux, so reading
	// those fields directly does not compile on both.
	info      os.FileInfo
	lastCheck time.Time
}

// Open opens path for appending, creating it (and its parent directory) if
// needed.
func Open(path string) (*Writer, error) {
	w := &Writer{path: path}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.openLocked(); err != nil {
		return nil, err
	}
	return w, nil
}

// Path returns the log path Writer was opened with.
func (w *Writer) Path() string { return w.path }

// Write appends p to the log, reopening first if the log was rotated away.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, ErrClosed
	}
	w.reopenIfRotatedLocked()
	return w.file.Write(p)
}

// Close flushes and releases the underlying file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// openLocked (re)opens the log in append mode and records the identity of the
// inode it now holds, which reopenIfRotatedLocked compares against later.
func (w *Writer) openLocked() error {
	dir := filepath.Dir(w.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating log directory %s: %w", dir, err)
	}
	// 0600, matching the mode in etc/newsyslog.d/<name>.conf so a rotation
	// does not silently widen it. These logs can carry principals, account
	// names and proxy hosts; nothing else needs to read them.
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening log file %s: %w", w.path, err)
	}
	if w.file != nil {
		_ = w.file.Close()
	}
	w.file = f

	w.info = nil
	if fi, statErr := f.Stat(); statErr == nil {
		w.info = fi
	}
	w.lastCheck = time.Now()
	return nil
}

// reopenIfRotatedLocked reopens the log when the path no longer resolves to
// the inode we hold — i.e. newsyslog renamed it — or when it has disappeared
// entirely. Errors are deliberately swallowed: losing the ability to log is
// not a reason to take the daemon down, and the next write retries.
func (w *Writer) reopenIfRotatedLocked() {
	if time.Since(w.lastCheck) < checkInterval {
		return
	}
	w.lastCheck = time.Now()

	fi, err := os.Stat(w.path)
	if err != nil {
		if os.IsNotExist(err) {
			_ = w.openLocked()
		}
		return
	}
	if w.info == nil || !os.SameFile(fi, w.info) {
		_ = w.openLocked()
	}
}
