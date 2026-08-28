package tunnel

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"

	"github.com/schretzi/tunneling/internal/config"

	"github.com/cedws/iapc/iap"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// serveIAP listens locally and forwards each accepted connection to the
// configured Compute Engine instance through Google Cloud IAP.
//
// Credentials come from Application Default Credentials, so
// `gcloud auth application-default login` has to have been run. The token
// source is resolved once here rather than per connection: it refreshes
// itself, and resolving it up front turns a missing login into a startup
// error instead of a failure on the first connection.
func serveIAP(ctx context.Context, cfg *config.Config, t config.Tunnel) error {
	tokenSource, err := google.DefaultTokenSource(ctx)
	if err != nil {
		return fmt.Errorf("resolving Google application default credentials "+
			"(run `gcloud auth application-default login`): %w", err)
	}

	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", cfg.LocalAddr(t))
	if err != nil {
		return err
	}
	// Unblocks the Accept below on shutdown; Accept then returns
	// net.ErrClosed, which the loop treats as a clean stop.
	context.AfterFunc(ctx, func() { _ = listener.Close() })

	log.Printf("tunnel %s: IAP listening on %s -> %s (project %s, zone %s, %s)",
		t.Name, listener.Addr(), t.Endpoint(), t.Project, t.Zone, t.Nic)

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accepting on %s: %w", listener.Addr(), err)
		}
		go forwardIAP(ctx, t, conn, &tokenSource)
	}
}

// forwardIAP dials IAP for one accepted connection and copies bytes in both
// directions until either side closes.
//
// Errors here are per-connection and never fatal: a token blip or an instance
// that is down should cost you that one connection, not every tunnel.
func forwardIAP(ctx context.Context, t config.Tunnel, local net.Conn, tokenSource *oauth2.TokenSource) {
	defer func() { _ = local.Close() }()

	remote, err := iap.Dial(ctx,
		iap.WithProject(t.Project),
		iap.WithInstance(t.RemoteHost, t.Zone, t.Nic),
		iap.WithPort(t.RemotePort.String()),
		iap.WithTokenSource(tokenSource),
	)
	if err != nil {
		log.Printf("tunnel %s: dialing IAP for %s: %v", t.Name, t.Endpoint(), err)
		return
	}
	defer func() { _ = remote.Close() }()

	log.Printf("tunnel %s: connected %s -> %s", t.Name, local.RemoteAddr(), t.Endpoint())
	moved := pipe(ctx, t.Name, local, remote)
	log.Printf("tunnel %s: disconnected %s (%d bytes)", t.Name, local.RemoteAddr(), moved.total())
}
