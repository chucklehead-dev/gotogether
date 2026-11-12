package simlog_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/simlog"
)

// Requests in one buffered batch remain separate scheduling opportunities.
// A process crash after the first acknowledgment must be able to stop the
// second request, rather than making the entire batch indivisible.
func TestProduceBatchCanBeInterruptedBetweenRequests(t *testing.T) {
	for _, crash := range []bool{false, true} {
		t.Run(fmt.Sprintf("crash=%t", crash), func(t *testing.T) {
			acknowledged := 0
			faults := &qenv.Script{Progress: func() int { return acknowledged }}
			if crash {
				faults.Faults = []qenv.Scripted{{Kind: "nemesis.interrupt", After: 1}}
			}
			s := qenv.New(faults)
			defer s.Close()
			log := simlog.New(s, "events", 1)
			producer := s.Process("producer", false, func(ctx context.Context) {
				p := log.Producer(true)
				for _, value := range []string{"first", "second"} {
					p.Produce(ctx, "key", []byte(value), func(err error) {
						if err != nil {
							t.Errorf("produce: %v", err)
						}
						acknowledged++
					})
				}
				p.Flush(ctx)
			})
			s.Nemesis("interrupt", func() bool { return acknowledged > 0 && !producer.Down() }, producer.Crash)
			if err := s.Run(time.Second); err != nil {
				t.Fatal(err)
			}
			want := "first second"
			count := 2
			if crash {
				want, count = "first", 1
			}
			if got := values(log, 0); got != want || acknowledged != count || producer.Down() != crash {
				t.Fatalf("records=%q acknowledgments=%d down=%t; want %q, %d, %t", got, acknowledged, producer.Down(), want, count, crash)
			}
			if remaining, misnamed := faults.Remaining(), faults.Misnamed(); len(remaining) != 0 || len(misnamed) != 0 {
				t.Fatalf("interruption was not exercised: remaining=%v misnamed=%v", remaining, misnamed)
			}
		})
	}
}

// Assignment uses stable member names, so the same group does not give a
// different member a partition merely because its consumers joined first.
func TestGroupAssignmentIndependentOfJoinOrder(t *testing.T) {
	for _, order := range [][]string{{"a", "b", "z"}, {"z", "b", "a"}, {"b", "a", "z"}, {"b", "z", "a"}, {"a", "z", "b"}, {"z", "a", "b"}} {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			finished := false
			sim(t, qenv.Nominal{}, 3, func(ctx context.Context, log *simlog.Log) {
				keys := make([]string, 3)
				for i := range 100 {
					key := fmt.Sprintf("key%d", i)
					keys[log.Partition(key)] = key
				}
				p := log.Producer(true)
				acknowledged := 0
				for part, key := range keys {
					if key == "" {
						t.Fatalf("no setup key for partition %d", part)
					}
					p.Produce(ctx, key, []byte(fmt.Sprint(part)), func(err error) {
						if err != nil {
							t.Errorf("fixture produce: %v", err)
							return
						}
						acknowledged++
					})
				}
				p.Flush(ctx)
				if acknowledged != len(keys) {
					t.Fatalf("fixture acknowledgments=%d, want %d", acknowledged, len(keys))
				}
				members := map[string]*simlog.Member{}
				for _, name := range order {
					m := log.Member(ctx, name)
					members[name] = m
					if _, err := m.Poll(ctx); err != nil {
						t.Fatal(err)
					}
				}
				for part, name := range []string{"a", "b", "z"} {
					m := members[name]
					// Refresh the older members' assignments, then prove ownership
					// via Commit; reads from before the last join may be replayed.
					if name != order[len(order)-1] {
						if _, err := m.Poll(ctx); err != nil {
							t.Fatal(err)
						}
					}
					if err := m.Commit(ctx, part, 1); err != nil {
						t.Fatalf("member %s must own partition %d: %v", name, part, err)
					}
					if got := log.Committed(part); got != 1 {
						t.Fatalf("partition %d committed=%d, want 1", part, got)
					}
				}
				finished = true
			})
			if !finished {
				t.Fatal("consumers did not finish refreshing and committing their assignments")
			}
		})
	}
}
