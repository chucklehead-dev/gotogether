package simlog_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/simlog"
)

// script makes each named fault kind's choices, in turn, as the options
// listed, and every other choice nominal.
type script map[string][]int

func (s script) Choose(kind string, n int) int {
	k := strings.TrimPrefix(kind, "fault.")
	if os := s[k]; len(os) > 0 && qenv.IsFault(kind) {
		s[k] = os[1:]
		return os[0]
	}
	return 0
}

// sim runs fn as a task of a simulation chosen by c, with a log "events"
// of n partitions, for a second of virtual time.
func sim(t *testing.T, c qenv.Chooser, n int, fn func(ctx context.Context, l *simlog.Log)) *simlog.Log {
	t.Helper()
	s := qenv.New(c)
	defer s.Close()
	l := simlog.New(s, "events", n)
	s.Spawn("test", func(ctx context.Context) { fn(ctx, l) })
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	return l
}

// produce sends values of key through w and waits for them.
func produce(ctx context.Context, w simlog.Writer, key string, values ...string) {
	for _, v := range values {
		w.Produce(ctx, key, []byte(v), func(error) {})
	}
	w.Flush(ctx)
}

// values are partition p's values, in offset order.
func values(l *simlog.Log, p int) string {
	var vs []string
	for _, r := range l.Records(p) {
		vs = append(vs, string(r.Value))
	}
	return strings.Join(vs, " ")
}

// A key's records land in one partition, in the order produced, and a
// consumer reads them from offset 0 with no holes. The log keeps its own
// copy of a value, and hands out copies.
func TestProduceAndPoll(t *testing.T) {
	var read []simlog.Record
	l := sim(t, script{}, 2, func(ctx context.Context, l *simlog.Log) {
		p := l.Producer(true)
		v := []byte("a1")
		p.Produce(ctx, "a", v, func(error) {})
		v[1] = 'X' // the caller reuses its buffer
		produce(ctx, p, "a", "a2", "a3")
		produce(ctx, p, "b", "b1")
		m := l.Member(ctx, "c1")
		for len(read) < 4 {
			recs, err := m.Poll(ctx)
			if err != nil {
				t.Fatal(err)
			}
			read = append(read, recs...)
		}
		read[0].Value[0] = 'Z' // a consumer changes what it read
	})
	pa, pb := l.Partition("a"), l.Partition("b")
	if got := values(l, pa); !strings.HasPrefix(got, "a1 a2 a3") {
		t.Errorf("partition %d: %s, want a1 a2 a3 in order", pa, got)
	}
	if got := values(l, pb); !strings.Contains(got, "b1") {
		t.Errorf("partition %d: %s, want b1", pb, got)
	}
	next := map[int]int{}
	for _, r := range read {
		if r.Offset != next[r.Partition] {
			t.Errorf("read %+v, want offset %d of partition %d", r, next[r.Partition], r.Partition)
		}
		next[r.Partition]++
	}
}

// A lost produce request is retried; an idempotent producer keeps the
// key's order (the record sent behind it is refused out of order, then
// retried after it), a plain one lands it after the next record.
func TestRetriesAndIdempotence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		idempotent bool
		faults     []int // the first requests' produce faults
		want       string
	}{
		{"lost, idempotent", true, []int{1}, "1 2"},
		{"lost, plain", false, []int{1}, "2 1"},
		{"lostAck, idempotent", true, []int{2}, "1 2"},
		{"lostAck, plain", false, []int{2}, "1 2 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var errs []error
			l := sim(t, script{"log.events.produce": tc.faults}, 1, func(ctx context.Context, l *simlog.Log) {
				p := l.Producer(tc.idempotent)
				for _, v := range []string{"1", "2"} {
					p.Produce(ctx, "k", []byte(v), func(err error) { errs = append(errs, err) })
				}
				p.Flush(ctx)
			})
			if got := values(l, 0); got != tc.want {
				t.Errorf("landed %q, want %q", got, tc.want)
			}
			if len(errs) != 2 || errs[0] != nil || errs[1] != nil {
				t.Errorf("acknowledgements %v, want two, each nil (the producer retries)", errs)
			}
		})
	}
}

// A record produced while the sender is sending its partition's batch
// (the sender yields before each request) is sent after it, not dropped:
// the sender once put back the partition's queue as it was when the
// batch began, losing the record, and then spun at one virtual instant
// with a record outstanding and none to send, past MaxSteps, so Run
// never returned (ci/consumer's gRPC handler, retried by its client,
// produced meanwhile). The sender now panics on that state instead.
func TestProduceWhileSending(t *testing.T) {
	// every schedule of the first steps among the runnable tasks: some
	// run the second Produce between the sender's batch and its end
	for sched := range 1 << 6 {
		t.Run(fmt.Sprintf("schedule %06b", sched), func(t *testing.T) {
			acks := 0
			l := sim(t, &schedule{bits: sched}, 1, func(ctx context.Context, l *simlog.Log) {
				p := l.Producer(true)
				ack := func(err error) {
					if err == nil {
						acks++
					}
				}
				p.Produce(ctx, "k", []byte("1"), ack)
				qenv.Yield(ctx)
				p.Produce(ctx, "k", []byte("2"), ack)
				p.Flush(ctx)
			})
			if got := values(l, 0); got != "1 2" || acks != 2 {
				t.Errorf("landed %q with %d acknowledgements, want \"1 2\" with 2", got, acks)
			}
		})
	}
}

// schedule picks among the runnable tasks by its bits, lowest first, for
// the first six scheduling steps; every other choice is nominal.
type schedule struct{ bits, n int }

func (s *schedule) Choose(kind string, n int) int {
	if kind != "sched.task" || s.n >= 6 {
		return 0
	}
	s.n++
	return (s.bits >> (s.n - 1) & 1) % n
}

// A commit that lands calls OnCommit, its acknowledgement lost or not; a
// lost one does not land. A rewound fetch reads again from the committed
// offset.
func TestCommitsAndRewind(t *testing.T) {
	var commits []string
	var errs []error
	var reread []simlog.Record
	sim(t, script{"log.events.commit": {0, 1, 2}, "log.events.fetch": {0, 1}}, 1, func(ctx context.Context, l *simlog.Log) {
		l.OnCommit = func(_ context.Context, p, next int) { commits = append(commits, fmt.Sprint(p, ":", next)) }
		produce(ctx, l.Producer(true), "k", "1", "2", "3")
		m := l.Member(ctx, "c1")
		l.Batch = 3
		if _, err := m.Poll(ctx); err != nil { // fetch 0: nominal
			t.Fatal(err)
		}
		for _, next := range []int{1, 2, 3} { // landed, lost, landed with its acknowledgement lost
			errs = append(errs, m.Commit(ctx, 0, next))
		}
		produce(ctx, l.Producer(true), "k", "4")
		reread, _ = m.Poll(ctx) // fetch 1: rewound to the committed offset
		if got := l.Committed(0); got != 3 {
			t.Errorf("committed %d, want 3", got)
		}
	})
	if strings.Join(commits, " ") != "0:1 0:3" {
		t.Errorf("OnCommit saw %v, want 0:1 and 0:3", commits)
	}
	if errs[0] != nil || !errors.Is(errs[1], simlog.ErrLost) || !errors.Is(errs[2], simlog.ErrLostAck) {
		t.Errorf("commit errors %v, want nil, ErrLost, ErrLostAck", errs)
	}
	if len(reread) != 1 || reread[0].Offset != 3 {
		t.Errorf("after the rewind read %+v, want offset 3 again from the committed offset", reread)
	}
}

// A second consumer's join moves the group's generation on: the first is
// fenced until it polls again, then reads its new assignment from the
// committed offsets.
func TestRebalanceFences(t *testing.T) {
	var fenced, after error
	var reread []simlog.Record
	sim(t, script{}, 2, func(ctx context.Context, l *simlog.Log) {
		l.Batch = 10
		p := l.Producer(true)
		for i := range 6 {
			produce(ctx, p, fmt.Sprint("k", i), "v")
		}
		m1 := l.Member(ctx, "c1")
		recs, _ := m1.Poll(ctx) // c1 alone: both partitions
		if err := m1.Commit(ctx, 0, 1); err != nil {
			t.Fatal(err)
		}
		m2 := l.Member(ctx, "c2")
		_, _ = m2.Poll(ctx) // c2 joins: a new generation
		fenced = m1.Commit(ctx, recs[len(recs)-1].Partition, recs[len(recs)-1].Offset+1)
		reread, _ = m1.Poll(ctx) // c1 rejoins: partition 0 alone, from its commit
		after = m1.Commit(ctx, 0, 2)
	})
	if !errors.Is(fenced, simlog.ErrFenced) || after != nil {
		t.Errorf("commit after the rebalance: %v, after rejoining: %v; want ErrFenced, then nil", fenced, after)
	}
	if len(reread) == 0 || reread[0].Partition != 0 || reread[0].Offset != 1 {
		t.Errorf("after rejoining read %+v, want partition 0 from offset 1", reread)
	}
}

// run is a pipeline on a log: a publisher and consumers in crashable
// processes, under c's faults and rebalances, with the log's contract
// checked (Sim.Check); it returns the panic it ended in, or "".
func run(c qenv.Chooser, broken string) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	s := qenv.New(c)
	defer s.Close()
	s.Check = true
	l := simlog.New(s, "events", 2)
	if broken != "" {
		simlog.Break(l, broken)
	}
	s.OnCrash(func(p *qenv.Process) { p.Restart() })
	s.Process("publisher", true, func(ctx context.Context) {
		p := l.Producer(true)
		for i := range 8 {
			p.Produce(ctx, fmt.Sprint("k", i%3), []byte(fmt.Sprint(i)), func(error) {})
		}
		p.Flush(ctx)
	})
	for _, name := range []string{"c1", "c2"} {
		s.Process(name, true, func(ctx context.Context) {
			m := l.Member(ctx, name)
			for {
				recs, _ := m.Poll(ctx)
				next := map[int]int{}
				for _, r := range recs {
					next[r.Partition] = r.Offset + 1
				}
				for p := range 2 {
					if n, ok := next[p]; ok {
						_ = m.Commit(ctx, p, n)
					}
				}
			}
		})
	}
	if err := s.Run(2 * time.Second); err != nil {
		return err.Error()
	}
	return ""
}

// Under faults, crashes and rebalances the log keeps its contract; each
// way of breaking it is caught by the check.
func TestContractCheck(t *testing.T) {
	seeded := func(seed uint64) qenv.Chooser {
		c := qenv.NewSeeded(seed)
		c.Rates = qenv.DefaultRates.Set("log", 0.3).Set("nemesis", 0.02)
		return c
	}
	for seed := range uint64(40) {
		if msg := run(seeded(seed), ""); msg != "" {
			t.Fatalf("seed %d: the faithful log failed its check: %s", seed, msg)
		}
	}
	for _, tc := range []struct{ broken, want string }{
		{"dedupe", "landed twice"},
		{"holes", "a hole"},
		{"fence", "does not own"},
	} {
		caught := ""
		for seed := uint64(0); seed < 40 && caught == ""; seed++ {
			caught = run(seeded(seed), tc.broken)
		}
		if !strings.Contains(caught, tc.want) || !strings.Contains(caught, "GOTOGETHER_CHECK") {
			t.Errorf("a log broken by %s: %q, want the check's %q", tc.broken, caught, tc.want)
		}
	}
}
