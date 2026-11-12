package qenv_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// An early signal ends only its own timed wait. The next deadline remains
// independent, equal deadlines follow creation order under Nominal, and an
// obsolete timer must not move the clock after all work has completed.
func TestTimedWaitAfterEarlyNotification(t *testing.T) {
	for _, tc := range []struct {
		name    string
		notify  bool
		firstAt time.Duration
	}{
		{"notified", true, 10 * time.Millisecond},
		{"times out", false, 100 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			s.Check = true
			defer s.Close()
			var first, second qenv.Signal
			type event struct {
				what string
				at   time.Duration
			}
			var events []event
			firstNotified, secondNotified := false, true
			finished, peerFinished := false, false
			lastAt := tc.firstAt + 20*time.Millisecond
			s.Spawn("waiter", func(ctx context.Context) {
				firstNotified = first.Wait(ctx, 100*time.Millisecond)
				events = append(events, event{"first", s.Elapsed()})
				secondNotified = second.Wait(ctx, 20*time.Millisecond)
				events = append(events, event{"second", s.Elapsed()})
				finished = true
			})
			s.Spawn("peer", func(ctx context.Context) {
				qenv.Sleep(ctx, lastAt)
				events = append(events, event{"peer", s.Elapsed()})
				peerFinished = true
			})
			s.Spawn("notifier", func(ctx context.Context) {
				qenv.Sleep(ctx, 10*time.Millisecond)
				if tc.notify {
					first.Notify()
				}
			})
			if err := s.Run(time.Second); err != nil {
				t.Fatal(err)
			}
			if !finished || !peerFinished {
				t.Fatal("timed work did not complete within the simulation horizon")
			}
			if firstNotified != tc.notify || secondNotified {
				t.Errorf("notification results = %t, %t; want %t, false", firstNotified, secondNotified, tc.notify)
			}
			want := []event{{"first", tc.firstAt}, {"second", lastAt}, {"peer", lastAt}}
			if !slices.Equal(events, want) {
				t.Errorf("deadline events = %v, want %v", events, want)
			}
			if got := s.Elapsed(); got != lastAt {
				t.Errorf("quiet simulation stopped at %v, want %v", got, lastAt)
			}
		})
	}
}
