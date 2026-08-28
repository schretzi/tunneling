// Package daemon implements the foreground process behind the `daemon`
// subcommand: the one the LaunchAgent runs.
package daemon

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/schretzi/tunneling/internal/config"
	"github.com/schretzi/tunneling/internal/tunnel"
)

// Run loads configPath and serves every selected tunnel (all of them if
// tunnelNames is empty) until it receives SIGTERM/SIGINT or ctx is canceled.
//
// The config is read once, at startup. Unlike a poller there is nothing to
// re-read between: each tunnel owns a bound listener for the process's whole
// life, so a changed port or destination can only take effect by rebinding —
// which is a restart. Use `tunneling service restart` after editing the
// config.
//
// Whatever error it returns is also written to the daemon's own log before
// returning — see the deferred logger below.
func Run(ctx context.Context, configPath string, tunnelNames []string) (err error) {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Load the config first, but do not fail on it yet: its error is the most
	// likely reason for a restart loop, and it deserves to be logged like any
	// other. Only the log *path* is needed before that can happen, and there
	// is a usable default when the config is what is broken.
	cfg, cfgErr := config.Load(configPath)
	logPath := config.DefaultLogPath()
	if cfgErr == nil {
		logPath = cfg.Daemon.Log.Path
	}

	logw, logErr := setupLogging(logPath)
	if logErr != nil {
		// Nowhere to log this but stderr, which is the one case
		// tunneling.err.log genuinely exists for.
		return logErr
	}
	defer func() { _ = logw.Close() }()

	// Log the fatal error as well as returning it.
	//
	// Returning it alone only reaches stderr, which launchd captures into
	// ~/Library/Logs/tunneling.err.log — the file documented as "crash
	// capture only, normally empty". Under KeepAlive a persistent failure
	// restarts every 10 seconds forever, and tunneling.log, the file everyone
	// is told to read, would otherwise show nothing but a wall of repeating
	// "daemon starting" lines with no reason attached to any of them.
	defer func() {
		if err != nil {
			log.Printf("fatal: %v", err)
		}
	}()

	if cfgErr != nil {
		return cfgErr
	}
	selected, err := cfg.SelectTunnels(tunnelNames)
	if err != nil {
		return err
	}

	// logw.Path(), not cfg.Daemon.Log.Path: the configured value may still be
	// a literal "~/...", and a log line naming a path you cannot `tail` is
	// worse than no log line.
	log.Printf("daemon starting: %d tunnel(s), bindAddress=%s, log=%s",
		len(selected), cfg.BindAddress, logw.Path())

	err = tunnel.Run(ctx, cfg, selected)
	if ctx.Err() != nil {
		log.Println("received shutdown signal, exiting")
		// A tunnel erroring out *because* we are shutting down is not a
		// failure to report; the signal is the reason it stopped.
		return nil
	}
	return err
}
