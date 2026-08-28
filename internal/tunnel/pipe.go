package tunnel

import (
	"context"
	"io"
	"log"
	"net"
)

// transferred is how much a single forwarded connection moved, in each
// direction.
type transferred struct {
	FromRemote int64
	ToRemote   int64
}

// total is the bytes moved in both directions.
func (t transferred) total() int64 { return t.FromRemote + t.ToRemote }

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
func pipe(ctx context.Context, name string, local, remote net.Conn) transferred {
	var moved transferred

	done := make(chan struct{})
	go func() {
		defer close(done)
		n, err := io.Copy(local, remote)
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
