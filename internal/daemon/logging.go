package daemon

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/schretzi/tunneling/internal/config"
	"github.com/schretzi/tunneling/internal/logfile"

	"golang.org/x/term"
)

// setupLogging points the standard logger at the daemon's own log file.
//
// Rotation is newsyslog's job (etc/newsyslog.d/tunneling.conf in
// MacbookSetup), not this process's: a flapping VPN produces a connect/
// disconnect pair per tunnel per attempt, and newsyslog caps that by size.
// Because newsyslog rotates by renaming, the writer re-stats the path and
// reopens when the file moves — otherwise the daemon would keep writing into
// the archived inode and the live log would stay empty forever.
//
// launchd's StandardErrorPath still captures this process's raw stderr, but
// once this returns those files only receive output that bypasses the logger
// entirely: panics, and anything written before setup. The log file is the
// one to read.
//
// When stderr is a terminal the log is also echoed there. Unlike a background
// poller, `tunneling daemon` is routinely run by hand to watch tunnels come
// up, and a foreground command that prints nothing at all looks hung. Under
// launchd stderr is a file, so this does not double-write the log.
//
// The returned writer flushes and releases the file on Close, and reports the
// resolved log path.
//
// It takes the path rather than the whole config so that it can be set up
// before the config has been validated — a broken config is the likeliest
// cause of a restart loop, and its error has to be loggable too.
func setupLogging(logPath string) (*logfile.Writer, error) {
	path, err := config.ExpandPath(logPath)
	if err != nil {
		return nil, fmt.Errorf("resolving daemon.log.path %s: %w", logPath, err)
	}
	w, err := logfile.Open(path)
	if err != nil {
		return nil, err
	}

	if term.IsTerminal(int(os.Stderr.Fd())) {
		log.SetOutput(io.MultiWriter(w, os.Stderr))
	} else {
		log.SetOutput(w)
	}
	return w, nil
}
