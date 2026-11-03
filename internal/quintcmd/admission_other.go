//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package quintcmd

import "context"

// slots is process-wide: platforms without flock coordinate within this process.
var slots = make(chan struct{}, 2)

func acquire(ctx context.Context) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	}
}
