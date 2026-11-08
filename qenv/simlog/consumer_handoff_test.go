package simlog_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/simlog"
)

// Losing a consumer revokes its assignment, fences its stale commits, and
// lets a surviving consumer replay only the uncommitted suffix. Exercise
// the named eviction separately from a process crash: either can happen
// without another consumer joining the group.
func TestConsumerHandoff(t *testing.T) {
	for _, cause := range []string{"eviction", "crash"} {
		t.Run(cause, func(t *testing.T) {
			phase := 0
			faults := &qenv.Script{Progress: func() int { return phase }}
			if cause == "eviction" {
				faults.Faults = []qenv.Scripted{{Kind: "nemesis.rebalance.events.a", After: 1}}
			}
			s := qenv.New(faults)
			defer s.Close()
			l := simlog.New(s, "events", 2)
			l.Batch = 10
			var commits []string
			l.OnCommit = func(_ context.Context, p, next int) {
				commits = append(commits, fmt.Sprintf("%d:%d", p, next))
			}
			prepared, joinedA, readyA, readyB, departed, finished := false, false, false, false, false, false
			var a *simlog.Member
			s.Spawn("produce", func(ctx context.Context) {
				keys := make([]string, 2)
				for i := range 16 {
					key := fmt.Sprint("key", i)
					keys[l.Partition(key)] = key
				}
				if keys[0] == "" || keys[1] == "" {
					t.Fatal("setup did not find a key for each partition")
				}
				for p, key := range keys {
					produce(ctx, l.Producer(true), key, fmt.Sprintf("p%d-first", p), fmt.Sprintf("p%d-second", p), fmt.Sprintf("p%d-third", p))
				}
				prepared = true
			})
			victim := s.Process("victim", false, func(ctx context.Context) {
				qenv.Wait(ctx, func() bool { return prepared })
				a = l.Member(ctx, "a")
				if recs, err := a.Poll(ctx); err != nil || len(recs) != 6 {
					t.Fatalf("first consumer setup: records=%v err=%v", recs, err)
				}
				joinedA = true
				qenv.Wait(ctx, func() bool { return readyB })
				if recs, err := a.Poll(ctx); err != nil || len(recs) != 3 || recs[0].Partition != 0 {
					t.Fatalf("first consumer refreshed assignment: records=%v err=%v", recs, err)
				}
				if err := a.Commit(ctx, 0, 1); err != nil {
					t.Fatal(err)
				}
				readyA = true
				qenv.WaitIdle(ctx, func() bool { return false }) // hold its assignment until the controller revokes it
			})
			survivor := s.Process("survivor", false, func(ctx context.Context) {
				qenv.Wait(ctx, func() bool { return joinedA })
				b := l.Member(ctx, "b")
				if recs, err := b.Poll(ctx); err != nil || len(recs) != 3 || recs[0].Partition != 1 {
					t.Fatalf("second consumer setup: records=%v err=%v", recs, err)
				}
				if err := b.Commit(ctx, 1, 1); err != nil {
					t.Fatal(err)
				}
				readyB = true
				qenv.Wait(ctx, func() bool { return departed })
				recs, err := b.Poll(ctx)
				var got []string
				for _, r := range recs {
					got = append(got, fmt.Sprintf("%d:%d:%s", r.Partition, r.Offset, r.Value))
				}
				want := []string{"0:1:p0-second", "0:2:p0-third", "1:1:p1-second", "1:2:p1-third"}
				if err != nil || !slices.Equal(got, want) {
					t.Fatalf("survivor replay: got %v err=%v, want %v", got, err, want)
				}
				for p := range 2 {
					if err := b.Commit(ctx, p, 3); err != nil {
						t.Fatalf("survivor commit partition %d: %v", p, err)
					}
				}
				finished = true
			})
			s.Spawn("depart", func(ctx context.Context) {
				qenv.Wait(ctx, func() bool { return readyA && readyB })
				if cause == "crash" {
					victim.Crash()
				} else {
					phase = 1
					qenv.Yield(ctx) // the now-due named eviction runs at this scheduling seam
				}
				before := len(commits)
				if err := a.Commit(ctx, 0, 3); !errors.Is(err, simlog.ErrFenced) {
					t.Errorf("departed consumer commit: %v, want ErrFenced", err)
				}
				if l.Committed(0) != 1 || l.Committed(1) != 1 || len(commits) != before {
					t.Errorf("fenced commit changed offsets or called OnCommit: offsets=%d,%d callbacks=%v", l.Committed(0), l.Committed(1), commits)
				}
				if survivor.Down() {
					t.Error("unrelated consumer process was crashed")
				}
				departed = true
			})
			if err := s.Run(time.Second); err != nil {
				t.Fatal(err)
			}
			if !finished {
				t.Error("surviving consumer did not complete handoff and commit both partitions")
			}
			if got, want := commits, []string{"1:1", "0:1", "0:3", "1:3"}; !slices.Equal(got, want) {
				t.Errorf("commits %v, want %v", got, want)
			}
			if remaining, misnamed := faults.Remaining(), faults.Misnamed(); len(remaining) != 0 || len(misnamed) != 0 {
				t.Errorf("fault was not exercised: remaining=%v misnamed=%v", remaining, misnamed)
			}
		})
	}
}
