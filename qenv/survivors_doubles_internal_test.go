package qenv

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Under Check, a queue checks its contract after every operation, so a
// bug is named at the operation that made it, not at whichever one ran
// next. Each case loses message 2 by hand just before the operation, and
// the check after it must name that operation.
func TestQueueChecksAfterEachOperation(t *testing.T) {
	lose := func(q *Queue) {
		for i, d := range q.pending {
			if d.ID == 2 {
				q.pending = append(q.pending[:i:i], q.pending[i+1:]...)
			}
		}
	}
	for _, c := range []struct {
		op string
		do func(ctx context.Context, q *Queue)
	}{
		{"a publish", func(ctx context.Context, q *Queue) { lose(q); q.Publish(ctx, 3) }},
		{"a receive", func(ctx context.Context, q *Queue) { lose(q); q.Receive(ctx) }},
		{"an acknowledgement", func(ctx context.Context, q *Queue) { d := q.Receive(ctx); lose(q); d.Ack(ctx) }},
		{"a nack", func(ctx context.Context, q *Queue) { d := q.Receive(ctx); lose(q); d.Nack(ctx) }},
	} {
		t.Run(c.op, func(t *testing.T) {
			s := New(Nominal{})
			defer s.Close()
			s.Check = true
			q := NewQueue(s, "jobs", nil)
			q.Ordered = true
			s.Spawn("client", func(ctx context.Context) {
				q.Publish(ctx, 1)
				q.Publish(ctx, 2)
				c.do(ctx, q)
			})
			got := func() (msg string) {
				defer func() { msg = fmt.Sprint(recover()) }()
				s.Run(time.Minute)
				return ""
			}()
			if want := "after " + c.op + ": message 2 was lost"; !strings.Contains(got, want) {
				t.Errorf("got %q, want it to say %q", got, want)
			}
		})
	}
}

// A check that finds several messages delivered after their
// acknowledgement names the smallest, in every run: its failure is the
// same however Go iterates the map of live messages.
func TestQueueCheckNamesTheSmallestRedelivered(t *testing.T) {
	q := &Queue{Name: "jobs", acked: map[int]bool{}, held: map[*Delivery]*Process{}}
	for id := 1; id <= 16; id++ {
		q.next = id
		q.acked[id] = true
		q.pending = append(q.pending, &Delivery{ID: id, q: q})
	}
	for range 50 {
		got := func() (msg string) {
			defer func() { msg = fmt.Sprint(recover()) }()
			q.check("a test")
			return ""
		}()
		if !strings.Contains(got, "message 1 is delivered again") {
			t.Fatalf("got %q, want message 1 named", got)
		}
	}
}
