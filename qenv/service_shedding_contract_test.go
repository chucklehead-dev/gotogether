package qenv_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Expired queued requests acquire no worker. Dropping several of them must
// leave an already-notified healthy peer able to run and capacity reusable.
func TestServiceSheddingLeavesHealthyQueuedWorkRunnable(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	s.Check = true
	defer s.Close()
	var handled []int
	svc := qenv.NewService(s, "shedding-reuse", nil, func(ctx context.Context, req int) (int, error) {
		handled = append(handled, req)
		return req, nil
	})
	svc.Capacity = &qenv.Capacity{Workers: 1, Queue: 3, Time: func() time.Duration { return time.Second }, Shed: true}
	completed, finished := 0, false
	s.Spawn("supervisor", func(ctx context.Context) {
		for request, timeout := range []time.Duration{10 * time.Second, 100 * time.Millisecond, 200 * time.Millisecond, 5 * time.Second} {
			qenv.Go(ctx, "client", func(ctx context.Context) {
				value, err := svc.Call(ctx, request, timeout)
				if request == 1 || request == 2 {
					if !errors.Is(err, qenv.ErrTimeout) {
						t.Errorf("expired request %d returned %v, want timeout", request, err)
					}
				} else if err != nil || value != request {
					t.Errorf("healthy request %d returned %d, %v", request, value, err)
				}
				completed++
			})
		}
		if !qenv.WaitFor(ctx, func() bool { return completed == 4 }, 5*time.Second) {
			t.Error("healthy queued peer did not finish after expired requests were shed")
			return
		}
		if svc.InSystem() != 0 || svc.Shed != 2 || svc.Rejected != 0 {
			t.Errorf("after queue drain: in-system=%d shed=%d rejected=%d", svc.InSystem(), svc.Shed, svc.Rejected)
		}
		if value, err := svc.Call(ctx, 4, 5*time.Second); err != nil || value != 4 {
			t.Errorf("reused capacity returned %d, %v", value, err)
		}
		finished = true
	})
	if err := s.Run(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	if !finished || completed != 4 {
		t.Fatalf("scenario incomplete: finished=%t initial replies=%d", finished, completed)
	}
	if want := []int{0, 3, 4}; !slices.Equal(handled, want) || svc.Calls != len(want) {
		t.Errorf("handled %v with %d calls, want %v", handled, svc.Calls, want)
	}
	if svc.InSystem() != 0 || s.Elapsed() != 3*time.Second {
		t.Errorf("completed work retained capacity/time: in-system=%d elapsed=%v", svc.InSystem(), s.Elapsed())
	}
}
