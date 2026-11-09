package model

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

const nativePulseInterval = 30 * time.Second

// nativeProgressWriter serializes only progress, never subprocess evidence.
// Its lifetime is one command; parent phase messages surround that command.
type nativeProgressWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *nativeProgressWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(b)
}

// startNativePulse reports elapsed waiting, not solver steps or proof progress.
// stop joins the writer before the command's caller can emit END or a report.
func startNativePulse(ctx context.Context, w io.Writer, progress *simulationProgress, interval time.Duration) func() {
	if w == nil || progress == nil {
		return func() {}
	}
	start := time.Now()
	ticker := time.NewTicker(interval)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				fmt.Fprintf(w, "simulate %s: RUN native verification elapsed=%s\n", progress.binding, time.Since(start).Round(time.Millisecond))
			}
		}
	}()
	return sync.OnceFunc(func() {
		close(stop)
		<-done
	})
}
