package qobs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
)

// A recorder's seq order is its steps' time order, however two
// goroutines' steps interleave: the first goroutine's clock read is held
// until the second has recorded (or 200ms, if the recorder makes it
// wait). Taken apart, the first step was numbered first and timed last,
// which left webjobs' double-clicked submit's within bound unjudged
// ("the steps' times go back along the order checked"). Not parallel:
// the clock is process-wide.
func TestSeqOrderIsTimeOrder(t *testing.T) {
	var (
		mu     sync.Mutex
		held   int64 = -1 // the goroutine whose next clock read waits
		second       = make(chan struct{})
	)
	hooks.SetSite(func(context.Context) hooks.Site {
		return hooks.Site{Clock: clockFunc(func() time.Time {
			mu.Lock()
			hold := held == hooks.GoroutineID()
			if hold {
				held = -1
			}
			mu.Unlock()
			if hold {
				select {
				case <-second:
				case <-time.After(200 * time.Millisecond):
				}
			}
			return time.Now() // the wall clock's
		})}
	})
	t.Cleanup(func() { hooks.SetSite(func(context.Context) hooks.Site { return hooks.Site{} }) })

	for _, kind := range []string{"record", "invoke", "done"} {
		t.Run(kind, func(t *testing.T) {
			mem := &MemorySink{}
			r := NewRecorder(mem)
			ctx := context.Background()
			var calls [2]*Call
			if kind == "done" {
				calls[0], calls[1] = r.Invoke(ctx, "a"), r.Invoke(ctx, "b")
			}
			step := func(i int) {
				switch kind {
				case "record":
					r.Record(ctx, "a", nil)
				case "invoke":
					r.Invoke(ctx, "a")
				case "done":
					calls[i].Done(nil)
				}
			}
			second = make(chan struct{})
			started := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				mu.Lock()
				held = hooks.GoroutineID()
				mu.Unlock()
				close(started)
				step(0)
			}()
			<-started
			// the first goroutine is in its clock read, or about to be
			time.Sleep(20 * time.Millisecond)
			step(1)
			close(second)
			wg.Wait()

			steps := mem.Steps()
			for i := range steps {
				for j := range steps {
					if steps[i].Seq < steps[j].Seq && steps[j].Time.Before(steps[i].Time) {
						t.Errorf("seq %d is timed %v, after seq %d's %v", steps[i].Seq, steps[i].Time.Format(time.StampMicro),
							steps[j].Seq, steps[j].Time.Format(time.StampMicro))
					}
				}
			}
		})
	}
}

// clockFunc is a function as a hooks.Clock.
type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }
