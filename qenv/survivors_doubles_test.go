package qenv_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// answers makes the choices of the kinds it names (full chooser kinds,
// "fault.rpc.pay.request") and every other choice nominal.
type answers map[string]int

func (a answers) Choose(kind string, n int) int { return a[kind] }

// runRecovering runs s to horizon and returns the panic a task ended in
// (Sim.Run re-raises it), or "".
func runRecovering(s *qenv.Sim, horizon time.Duration) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	if err := s.Run(horizon); err != nil {
		return err.Error()
	}
	return ""
}

// crashMidway runs body in process "node" of s, crashing the process
// from another task as soon as body calls ready: the crasher is created
// first, so the nominal schedule runs it at the first seam after ready.
// attach builds the doubles of the process before the run. It reports
// whether body ran to its end.
func crashMidway(t *testing.T, s *qenv.Sim, attach func(p *qenv.Process), body func(ctx context.Context, ready func())) (finished bool) {
	t.Helper()
	armed := false
	var p *qenv.Process
	s.Spawn("crasher", func(ctx context.Context) {
		qenv.Wait(ctx, func() bool { return armed })
		p.Crash()
	})
	p = s.Process("node", false, func(ctx context.Context) {
		body(ctx, func() { armed = true })
		finished = true
	})
	attach(p)
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	return finished
}

// Two disks, services or queues of one name in a simulation would share
// their fault kinds, so a rate or a script naming one would mean both:
// the second panics, naming the double and the fix.
func TestDoublesClaimTheirNames(t *testing.T) {
	for _, c := range []struct {
		what string
		make func(s *qenv.Sim)
	}{
		{"Disk", func(s *qenv.Sim) { qenv.NewDisk(s, "d", nil) }},
		{"Service", func(s *qenv.Sim) {
			qenv.NewService(s, "d", nil, func(context.Context, int) (int, error) { return 0, nil })
		}},
		{"Queue", func(s *qenv.Sim) { qenv.NewQueue(s, "d", nil) }},
	} {
		t.Run(c.what, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			defer s.Close()
			c.make(s)
			defer func() {
				r := fmt.Sprint(recover())
				if !strings.Contains(r, "a second "+c.what+` named "d"`) {
					t.Errorf("a second %s of one name: panic %q, want one naming it", c.what, r)
				}
			}()
			c.make(s)
		})
	}
}

// A file's durable part is what was synced, and Append continues a file
// that exists (creating one that does not): a log reopened after a crash
// keeps its records. A missing file has no durable part.
func TestDiskDurableAndAppend(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	var d *qenv.Disk
	p := s.Process("node", false, func(ctx context.Context) {
		f := d.Create("log")
		f.Write([]byte("one\n"))
		f.Sync()
		f.Write([]byte("two\n"))
		g := d.Append("log")
		g.Write([]byte("three\n"))
		n := d.Append("new")
		n.Write([]byte("x"))
	})
	d = qenv.NewDisk(s, "disk", p)
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.ReadFile("log"); string(got) != "one\ntwo\nthree\n" {
		t.Errorf("log after Append: %q, want every write", got)
	}
	if got := d.Durable("log"); string(got) != "one\n" {
		t.Errorf("Durable: %q, want the synced write", got)
	}
	if got, ok := d.ReadFile("new"); !ok || string(got) != "x" {
		t.Errorf("a file Append created: %q, %v", got, ok)
	}
	if got := d.Durable("missing"); got != nil {
		t.Errorf("Durable of a missing file: %q, want nil", got)
	}
}

// Write and Sync are seams: a crash may fall just before either. A
// process crash before a Write loses it (it never reached the OS); a power
// loss just before a Sync loses the write it was to make durable.
func TestDiskCrashBeforeWriteOrSync(t *testing.T) {
	for _, c := range []struct {
		name  string
		crash crashAs
		body  func(f *qenv.File, ready func())
	}{
		{"write", 0, func(f *qenv.File, ready func()) { // the process dies, the OS keeps its writes
			ready()
			f.Write([]byte("a"))
		}},
		{"sync", 1, func(f *qenv.File, ready func()) { // the machine loses power
			f.Write([]byte("a"))
			ready()
			f.Sync()
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := qenv.New(c.crash)
			defer s.Close()
			var d *qenv.Disk
			finished := crashMidway(t, s, func(p *qenv.Process) { d = qenv.NewDisk(s, "disk", p) },
				func(ctx context.Context, ready func()) { c.body(d.Create("log"), ready) })
			got, _ := d.ReadFile("log")
			if finished || string(got) != "" {
				t.Errorf("crashed before its %s: file %q (finished %v), want it empty", c.name, got, finished)
			}
		})
	}
}

// Publish and Ack are seams: a publisher crashed just before its publish
// published nothing, and a consumer crashed just before its
// acknowledgement holds the message, which is redelivered. Nack is a seam
// too: another task runs before the message is back in the queue.
func TestQueueOperationsAreSeams(t *testing.T) {
	t.Run("publish", func(t *testing.T) {
		s := qenv.New(qenv.Nominal{})
		defer s.Close()
		q := qenv.NewQueue(s, "jobs", nil)
		crashMidway(t, s, func(*qenv.Process) {}, func(ctx context.Context, ready func()) {
			ready()
			q.Publish(ctx, 1)
		})
		if q.Published != 0 || q.Pending() != 0 {
			t.Errorf("crashed before its publish: %d published, %d pending, want none", q.Published, q.Pending())
		}
	})
	t.Run("ack", func(t *testing.T) {
		s := qenv.New(qenv.Nominal{})
		defer s.Close()
		q := qenv.NewQueue(s, "jobs", nil)
		crashMidway(t, s, func(*qenv.Process) {}, func(ctx context.Context, ready func()) {
			q.Publish(ctx, 1)
			d := q.Receive(ctx)
			ready()
			d.Ack(ctx)
		})
		if q.Pending() != 1 || q.Redelivered != 1 {
			t.Errorf("crashed before its ack: %d pending, %d redelivered, want the message back", q.Pending(), q.Redelivered)
		}
	})
	t.Run("nack", func(t *testing.T) {
		s := qenv.New(qenv.Nominal{})
		defer s.Close()
		q := qenv.NewQueue(s, "jobs", nil)
		armed, seen := false, -1
		s.Spawn("observer", func(ctx context.Context) {
			qenv.Wait(ctx, func() bool { return armed })
			seen = q.Pending()
		})
		s.Spawn("consumer", func(ctx context.Context) {
			q.Publish(ctx, 1)
			d := q.Receive(ctx)
			armed = true
			d.Nack(ctx)
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		if seen != 0 || q.Pending() != 1 {
			t.Errorf("during the nack another task saw %d pending (want 0), after it %d (want 1)", seen, q.Pending())
		}
	})
}

// A cut between the broker and a consumer drops the consumer's
// connection, whichever side Partition names first: the messages it held
// are redelivered.
func TestQueueCutRedeliversEitherWay(t *testing.T) {
	for _, brokerFirst := range []bool{true, false} {
		s := qenv.New(qenv.Nominal{})
		broker := s.Process("broker", false, func(context.Context) {})
		q := qenv.NewQueue(s, "jobs", broker)
		took := false
		var c *qenv.Process
		c = s.Process("consumer", false, func(ctx context.Context) {
			q.Publish(ctx, 1)
			q.Receive(ctx)
			took = true
		})
		s.Spawn("cutter", func(ctx context.Context) {
			qenv.Wait(ctx, func() bool { return took })
			if brokerFirst {
				s.Net().Partition(broker, c)
			} else {
				s.Net().Partition(c, broker)
			}
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		if q.InFlight() != 0 || q.Pending() != 1 {
			t.Errorf("broker named first %v: %d in flight, %d pending after the cut, want the message back", brokerFirst, q.InFlight(), q.Pending())
		}
		s.Close()
	}
}

// Under Check, a queue that loses a crashed consumer's messages
// (BreakQueue) panics at the crash, naming it and the message lost: the
// newest one, behind one acknowledged. And it panics at the broker's
// crash, which puts the held messages back too.
func TestQueueCheckNamesTheLostMessage(t *testing.T) {
	t.Run("consumer", func(t *testing.T) {
		s := qenv.New(qenv.Nominal{})
		defer s.Close()
		s.Check = true
		q := qenv.NewQueue(s, "jobs", nil)
		qenv.BreakQueue(q)
		var c *qenv.Process
		c = s.Process("c", false, func(ctx context.Context) {
			q.Publish(ctx, 1)
			q.Receive(ctx).Ack(ctx)
			q.Publish(ctx, 2)
			q.Receive(ctx)
			c.Crash()
		})
		got := runRecovering(s, time.Minute)
		if want := "after dropping c's deliveries: message 2 was lost"; !strings.Contains(got, want) {
			t.Errorf("got %q, want it to say %q", got, want)
		}
	})
	t.Run("broker", func(t *testing.T) {
		s := qenv.New(qenv.Nominal{})
		defer s.Close()
		s.Check = true
		broker := s.Process("broker", false, func(context.Context) {})
		q := qenv.NewQueue(s, "jobs", broker)
		qenv.BreakQueue(q)
		s.Spawn("c", func(ctx context.Context) {
			q.Publish(ctx, 1)
			q.Receive(ctx)
			broker.Crash()
		})
		got := runRecovering(s, time.Minute)
		if want := "after the broker's crash: message 1 was lost"; !strings.Contains(got, want) {
			t.Errorf("got %q, want it to say %q", got, want)
		}
	})
}

// Call is a seam: a caller crashed just before it sent nothing, and the
// handler never runs.
func TestServiceCallerCrashedBeforeCall(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	svc := qenv.NewService(s, "pay", nil, func(context.Context, int) (int, error) { return 0, nil })
	crashMidway(t, s, func(*qenv.Process) {}, func(ctx context.Context, ready func()) {
		ready()
		svc.Call(ctx, 1, time.Second)
	})
	if svc.Calls != 0 {
		t.Errorf("the caller crashed before its call, yet the handler ran %d times", svc.Calls)
	}
}

// A dropped request is never processed, whatever the delay fault says: a
// late duplicate is a copy of a request that was sent, and a dropped one
// has none.
func TestServiceDroppedRequestHasNoLateCopy(t *testing.T) {
	s := qenv.New(answers{"fault.rpc.pay.request": 1, "fault.rpc.pay.delay": 2}) // drop, lateDuplicate
	defer s.Close()
	svc := qenv.NewService(s, "pay", nil, func(context.Context, int) (int, error) { return 0, nil })
	var err error
	s.Spawn("caller", func(ctx context.Context) { _, err = svc.Call(ctx, 1, time.Second) })
	if rerr := s.Run(time.Minute); rerr != nil {
		t.Fatal(rerr)
	}
	if !errors.Is(err, qenv.ErrTimeout) || svc.Calls != 0 {
		t.Errorf("a dropped request: %v, handler ran %d times; want a timeout and no call", err, svc.Calls)
	}
}
