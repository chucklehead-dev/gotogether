package qenv

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A service owns only the waiters of its active process incarnation. Filling
// and crashing several generations must not retain queued handlers in free's
// roster, even when no worker ever reaches its ordinary release path.
func TestServiceCapacityWaiterOwnershipAcrossCrashes(t *testing.T) {
	for _, phase := range []string{"service delay", "handler work"} {
		t.Run(phase, func(t *testing.T) {
			s := New(Nominal{})
			s.Check = true
			defer s.Close()
			server := s.Process("server", false, func(context.Context) {})
			serviceTime := 10 * time.Second
			if phase == "handler work" {
				serviceTime = time.Millisecond
			}
			svc := NewService(s, "capacity-ownership", server, func(ctx context.Context, req int) (int, error) {
				if phase == "handler work" && req > 0 {
					Sleep(ctx, 10*time.Second)
				}
				return req, nil
			})
			svc.Capacity = &Capacity{Workers: 1, Queue: 1, Time: func() time.Duration { return serviceTime }}
			checkWaiters := func(want int) {
				t.Helper()
				if got := len(svc.free.waiters); got != want {
					t.Errorf("capacity waiter roster has %d entries, want %d active owners", got, want)
				}
				for _, w := range svc.free.waiters {
					if w.t.proc != server || w.t.inc != server.Incarnation || w.t.done || w.t.killed || !w.t.waiting || w.gen != w.t.gen {
						t.Errorf("capacity waiter retained an inactive owner: incarnation=%d current=%d done=%t killed=%t", w.t.inc, server.Incarnation, w.t.done, w.t.killed)
					}
				}
			}
			const generations = 3
			oldFinished, healthyFinished, finished := 0, 0, false
			s.Spawn("supervisor", func(ctx context.Context) {
				for generation := range generations {
					for request := range 2 {
						req := 1 + generation*2 + request
						Go(ctx, "old client", func(ctx context.Context) {
							_, err := svc.Call(ctx, req, time.Minute)
							if !errors.Is(err, ErrTimeout) {
								t.Errorf("crashed request %d returned %v, want unknown-outcome timeout", req, err)
							}
							oldFinished++
						})
					}
					if !WaitFor(ctx, func() bool {
						return svc.InSystem() == 2 && (phase != "handler work" || svc.Calls == generation+1)
					}, time.Second) {
						t.Error("service never filled its worker and queue")
						return
					}
					checkWaiters(1)
					if _, err := svc.Call(ctx, 100, time.Second); !errors.Is(err, ErrOverloaded) {
						t.Errorf("full service returned %v, want overload", err)
					}
					server.Crash()
					if got := svc.InSystem(); got != 0 {
						t.Errorf("crashed service retains %d active requests", got)
					}
					checkWaiters(0)
					server.Restart()
				}
				serviceTime = time.Millisecond
				for req := range 2 {
					Go(ctx, "healthy client", func(ctx context.Context) {
						value, err := svc.Call(ctx, -req, time.Second)
						if err != nil || value != -req {
							t.Errorf("new-generation call returned %d, %v; want %d, nil", value, err, -req)
						}
						healthyFinished++
					})
				}
				if !WaitFor(ctx, func() bool { return healthyFinished == 2 }, time.Second) {
					t.Error("new-generation requests did not finish")
					return
				}
				if got := svc.InSystem(); got != 0 {
					t.Errorf("completed new-generation work retains %d requests", got)
				}
				checkWaiters(0)
				finished = true
			})
			if err := s.Run(2 * time.Minute); err != nil {
				t.Fatal(err)
			}
			if !finished || healthyFinished != 2 || oldFinished != 2*generations || svc.Rejected != generations {
				t.Fatalf("scenario incomplete: finished=%t healthy=%d old=%d rejected=%d", finished, healthyFinished, oldFinished, svc.Rejected)
			}
			s.Close() // old handler defers run now; they own no current-generation worker
			if got := svc.InSystem(); got != 0 {
				t.Errorf("old-generation deferred releases changed current capacity: %d requests", got)
			}
			checkWaiters(0)
		})
	}
}
