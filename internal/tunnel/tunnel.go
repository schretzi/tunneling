// Package tunnel runs the configured tunnels: a local listener per tunnel,
// forwarding either through Google Cloud IAP or through an SSH server.
//
// Run is the whole surface. It blocks until the context is canceled or a
// tunnel fails at the listener level, which is a deliberate all-or-nothing
// choice — see Run.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/schretzi/tunneling/internal/config"
	"github.com/schretzi/tunneling/internal/health"

	"golang.org/x/sync/errgroup"
)

// Run starts every tunnel in tunnels and blocks until ctx is canceled or one
// of them fails.
//
// Failure is all-or-nothing on purpose. A tunnel that cannot bind its port,
// or whose listener dies, takes the whole process down with a non-zero exit;
// under the LaunchAgent, launchd's KeepAlive then restarts everything after
// ThrottleInterval. Per-connection failures — a refused dial, a dropped
// copy — are logged and affect only that connection.
//
// Nothing is retried in-process: a half-up set of tunnels that silently
// stopped forwarding is far worse to debug than a process that exits loudly.
func Run(ctx context.Context, cfg *config.Config, tunnels []config.Tunnel, rec *health.Recorder) error {
	if len(tunnels) == 0 {
		return errors.New("no tunnels selected")
	}

	// The ssh-agent is only needed by `kind: ssh` tunnels. Checking up front
	// turns "the agent holds no keys" into a startup error instead of an
	// opaque authentication failure on the first connection.
	if config.HasKind(tunnels, config.KindSSH) {
		if err := checkSSHAgent(ctx); err != nil {
			return err
		}
	}

	g, ctx := errgroup.WithContext(ctx)
	for _, t := range tunnels {
		g.Go(func() error {
			if err := serve(ctx, cfg, t, rec); err != nil {
				return fmt.Errorf("tunnel %s: %w", t.Name, err)
			}
			log.Printf("tunnel %s: stopped", t.Name)
			return nil
		})
	}
	return g.Wait()
}

// serve dispatches one tunnel to its transport.
func serve(ctx context.Context, cfg *config.Config, t config.Tunnel, rec *health.Recorder) error {
	switch t.Kind {
	case config.KindGCP:
		return serveIAP(ctx, cfg, t, rec)
	case config.KindSSH:
		return serveSSH(ctx, cfg, t, rec)
	default:
		// Unreachable: config.Validate rejects any other kind at load time.
		return fmt.Errorf("unknown kind %q", t.Kind)
	}
}
