package tunnel

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"github.com/schretzi/tunneling/internal/health"
)

// transferred is how much a single forwarded connection moved, in each
// direction.
type transferred struct {
	FromRemote int64
	ToRemote   int64
}

// total is the bytes moved in both directions.
func (t transferred) total() int64 { return t.FromRemote + t.ToRemote }

// outcome classifies a completed forward for health reporting.
//
// The rule is about *data*, not errors, because errors here are ambiguous:
// closing the far end after the local client hangs up produces one, and so
// does a genuinely broken destination. What is unambiguous is whether the far
// end ever answered.
//
//	received > 0          the destination replied — the tunnel works
//	sent > 0, received 0  we spoke and got nothing back — the tunnel is broken
//	nothing either way    the client connected and left without saying
//	                      anything, so the destination was never asked. This
//	                      proves nothing, and it is exactly what `status`'s own
//	                      port probe looks like — counting it either way would
//	                      make the probe fake its own answer.
func (t transferred) outcome() (health.Outcome, string) {
	switch {
	case t.FromRemote > 0:
		return health.OutcomeSuccess, ""
	case t.ToRemote > 0:
		return health.OutcomeFailure, fmt.Sprintf("sent %d bytes, received nothing back", t.ToRemote)
	default:
		return health.OutcomeNeutral, ""
	}
}

// pipe copies between local and remote until either direction ends, closes
// both, and reports how much moved.
//
// Closing both is the point: whichever copy finishes first, the other is
// unblocked by the close rather than left holding a connection open. The
// previous SSH library did not do this — it started two copy goroutines and
// closed nothing until the whole tunnel shut down, which leaked an SSH client
// and its underlying IAP connection per forwarded connection.
//
// Copy errors are dropped once ctx is done: tearing the connection down is
// what produced them, and logging one per direction per open connection turns
// every shutdown into a wall of noise.
// onFirstReceive, if set, fires once as soon as any data arrives from the
// far end, before the connection finishes.
//
// Long-lived connections need this: a multiplexed SSH session over a gcp
// tunnel stays open for as long as the tunnel is used, so waiting for it to
// close would report the tunnel as IDLE the entire time it is busiest.
func pipe(ctx context.Context, name string, local, remote net.Conn, onFirstReceive func()) transferred {
	var moved transferred

	var src io.Reader = remote
	if onFirstReceive != nil {
		src = &firstByteNotifier{r: remote, fn: onFirstReceive}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		n, err := io.Copy(local, src)
		moved.FromRemote = n
		if err != nil && ctx.Err() == nil {
			log.Printf("tunnel %s: copy from remote: %v", name, err)
		}
		// Unblock the other direction: without this, a remote that closes
		// its side leaves the local->remote copy waiting on a client that
		// may never send again.
		_ = local.Close()
	}()

	n, err := io.Copy(remote, local)
	moved.ToRemote = n
	if err != nil && ctx.Err() == nil {
		log.Printf("tunnel %s: copy to remote: %v", name, err)
	}
	_ = remote.Close()

	// The channel close orders the goroutine's write to moved.FromRemote
	// before this read.
	<-done
	return moved
}

// firstByteNotifier calls fn the first time a read returns data.
//
// This does prevent io.Copy from reaching local.ReadFrom, but on darwin — the
// only platform this builds for — TCP-to-TCP ReadFrom has no zero-copy path
// to lose, so it falls back to the same buffered loop either way.
type firstByteNotifier struct {
	r    io.Reader
	once sync.Once
	fn   func()
}

func (f *firstByteNotifier) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if n > 0 {
		f.once.Do(f.fn)
	}
	return n, err
}
