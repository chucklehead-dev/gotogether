package qenv_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Two read-modify-write increments of one counter, run concurrently.
func increments(s *qenv.Sim, st *qenv.Store, forUpdate bool) {
	for i := 0; i < 2; i++ {
		s.Spawn(fmt.Sprintf("inc%d", i), func(ctx context.Context) {
			_ = qenv.Retry(ctx, st, 5, func(tx qenv.Txn) error {
				var v any
				var err error
				if forUpdate {
					v, _, err = tx.GetForUpdate("n")
				} else {
					v, _ = tx.Get("n")
				}
				if err != nil {
					return err
				}
				n, _ := v.(int)
				tx.Put("n", n+1)
				return nil
			})
		})
	}
}

// Two on-call doctors each go off call if the other is still on: the
// classic write skew, which leaves nobody on call.
func offCall(s *qenv.Sim, st *qenv.Store) {
	for _, me := range []string{"alice", "bob"} {
		other := map[string]string{"alice": "bob", "bob": "alice"}[me]
		s.Spawn(me, func(ctx context.Context) {
			_ = qenv.Retry(ctx, st, 5, func(tx qenv.Txn) error {
				if v, _ := tx.Get("oncall/" + other); v == true {
					tx.Put("oncall/"+me, false)
				}
				return nil
			})
		})
	}
}

func run(t *testing.T, c qenv.Chooser, iso qenv.Isolation, setup func(*qenv.Sim, *qenv.Store)) *qenv.Store {
	t.Helper()
	s := qenv.New(c)
	defer s.Close()
	st := qenv.NewStore(s, "db", iso)
	st.Load("oncall/alice", true)
	st.Load("oncall/bob", true)
	setup(s, st)
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	return st
}

// anomaly counts the seeds (of 200) whose run shows the anomaly.
func anomaly(t *testing.T, iso qenv.Isolation, setup func(*qenv.Sim, *qenv.Store), bad func(*qenv.Store) bool) int {
	n := 0
	for seed := uint64(0); seed < 200; seed++ {
		if bad(run(t, qenv.NewSeeded(seed), iso, setup)) {
			n++
		}
	}
	return n
}

func lostUpdate(st *qenv.Store) bool { v, _ := st.Read("n"); return v != 2 }
func nobodyOnCall(st *qenv.Store) bool {
	a, _ := st.Read("oncall/alice")
	b, _ := st.Read("oncall/bob")
	return a == false && b == false
}

// Each isolation level allows exactly its anomalies.
func TestIsolationAnomalies(t *testing.T) {
	plain := func(s *qenv.Sim, st *qenv.Store) { increments(s, st, false) }
	locked := func(s *qenv.Sim, st *qenv.Store) { increments(s, st, true) }
	cases := []struct {
		name  string
		iso   qenv.Isolation
		setup func(*qenv.Sim, *qenv.Store)
		bad   func(*qenv.Store) bool
		want  bool
	}{
		{"lost update, read committed", qenv.ReadCommitted, plain, lostUpdate, true},
		{"lost update, read committed, FOR UPDATE", qenv.ReadCommitted, locked, lostUpdate, false},
		{"lost update, snapshot isolation", qenv.SnapshotIsolation, plain, lostUpdate, false},
		{"write skew, snapshot isolation", qenv.SnapshotIsolation, offCall, nobodyOnCall, true},
		{"write skew, read committed", qenv.ReadCommitted, offCall, nobodyOnCall, true},
		{"write skew, serializable", qenv.Serializable, offCall, nobodyOnCall, false},
		{"lost update, serializable", qenv.Serializable, plain, lostUpdate, false},
	}
	for _, c := range cases {
		n := anomaly(t, c.iso, c.setup, c.bad)
		if (n > 0) != c.want {
			t.Errorf("%s: anomaly in %d of 200 seeded runs, want any: %v", c.name, n, c.want)
		}
	}
}

// The same seed gives the same run; Replay of its choices gives it again.
func TestDeterministicAndReplayable(t *testing.T) {
	var runs [][]qenv.Choice
	for i := 0; i < 2; i++ {
		rec := &qenv.ChoiceLog{C: qenv.NewSeeded(42)}
		run(t, rec, qenv.ReadCommitted, func(s *qenv.Sim, st *qenv.Store) { increments(s, st, false) })
		runs = append(runs, rec.Choices)
	}
	if fmt.Sprint(runs[0]) != fmt.Sprint(runs[1]) {
		t.Fatalf("same seed, different choices:\n%v\n%v", runs[0], runs[1])
	}
	rec := &qenv.ChoiceLog{C: qenv.Replay(runs[0])}
	run(t, rec, qenv.ReadCommitted, func(s *qenv.Sim, st *qenv.Store) { increments(s, st, false) })
	if fmt.Sprint(rec.Choices) != fmt.Sprint(runs[0]) {
		t.Fatalf("replay differs:\n%v\n%v", rec.Choices, runs[0])
	}
}

// A crash aborts the process's open transaction (releasing its lock) and
// redelivers the message it held; the restarted incarnation finishes.
func TestCrashReleasesAndRedelivers(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	st := qenv.NewStore(s, "db", qenv.ReadCommitted)
	q := qenv.NewQueue(s, "jobs")
	crashNext := true
	s.OnCrash(func(p *qenv.Process) { p.Restart() }) // a supervisor
	var p *qenv.Process
	p = s.Process("worker", false, func(ctx context.Context) {
		for {
			d := q.Receive(ctx)
			tx := st.Begin(ctx)
			if _, _, err := tx.GetForUpdate("job"); err != nil {
				t.Error(err)
			}
			tx.Put("job", d.Body)
			if crashNext {
				crashNext = false
				p.Crash() // the crashing task stops here
				t.Error("a crashed task ran on")
			}
			if err := tx.Commit(); err != nil {
				t.Error(err)
			}
			d.Ack(ctx)
		}
	})
	s.Spawn("client", func(ctx context.Context) { q.Publish(ctx, "j1") })
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if v, _ := st.Read("job"); v != "j1" || q.Redelivered != 1 || p.Incarnation != 2 {
		t.Fatalf("job=%v redelivered=%d incarnation=%d\n%s", v, q.Redelivered, p.Incarnation, s.Tasks())
	}
}

// A lost response is a timeout after the request took effect.
func TestLostResponseIsAmbiguous(t *testing.T) {
	applied := 0
	var got error
	h := &forced{faults: map[string]int{"fault.rpc.pay.response": 1}}
	s := qenv.New(h)
	defer s.Close()
	svc := qenv.NewService(s, "pay", nil, func(ctx context.Context, n int) (int, error) { applied += n; return applied, nil })
	s.Spawn("client", func(ctx context.Context) {
		_, got = svc.Call(ctx, 5, time.Second)
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(got, qenv.ErrTimeout) || applied != 5 {
		t.Fatalf("err=%v applied=%d", got, applied)
	}
	if s.Elapsed() != time.Second {
		t.Fatalf("virtual time %v, want the timeout", s.Elapsed())
	}
}

type forced struct{ faults map[string]int }

func (f *forced) Choose(kind string, n int) int { return f.faults[kind] }

// A timestamp taken from the real clock, translated to virtual time, ages
// with the simulation: time.Since of its translation back measures virtual
// time.
func TestClockTranslation(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	stored := s.ToVirtual(time.Now()) //qenv:ok a real timestamp, as a library would store one
	s.Spawn("sleeper", func(ctx context.Context) { qenv.Sleep(ctx, 45*time.Second) })
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if age := time.Since(s.ToReal(stored)); age < 44*time.Second || age > 46*time.Second { //qenv:ok read back as a library would
		t.Fatalf("aged %v in real time, want the 45s of virtual time", age)
	}
}

// Two tasks wait for the same lock: when it is released, only the first to
// run gets it; the other's predicate is false again, and it waits on.
func TestPredicateWaitersRecheck(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	held := true
	inside := 0
	for i := 0; i < 2; i++ {
		s.Spawn(fmt.Sprintf("waiter%d", i), func(ctx context.Context) {
			qenv.Wait(ctx, func() bool { return !held })
			held = true
			inside++
			if inside > 1 {
				t.Error("both waiters took the lock")
			}
		})
	}
	s.Spawn("releaser", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Second)
		held = false
	})
	// the other waits forever: the run is stuck, and says so
	if err := s.Run(time.Minute); !errors.Is(err, qenv.ErrStuck) {
		t.Fatalf("got %v, want ErrStuck", err)
	}
	if inside != 1 {
		t.Fatalf("%d waiters got the lock, want 1", inside)
	}
}

// Many waiting tasks cost the scheduler nothing per step: 20000 requests,
// each waiting on a Signal with a timeout, finish in well under a second.
func TestManyWaitersScale(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	sigs := make([]*qenv.Signal, 20000)
	answered := 0
	for i := range sigs {
		sigs[i] = &qenv.Signal{}
		s.Spawn("request", func(ctx context.Context) {
			if sigs[i].Wait(ctx, qenv.Now(ctx).Add(time.Hour)) {
				answered++
			}
		})
	}
	s.Spawn("server", func(ctx context.Context) {
		for _, g := range sigs {
			qenv.Sleep(ctx, time.Millisecond)
			g.Notify()
		}
	})
	t0 := time.Now() //qenv:ok measures the scheduler's real cost
	s.MaxSteps = 1 << 30
	s.Check = false // O(tasks) per step: this test is about scale
	if err := s.Run(time.Hour); err != nil {
		t.Fatal(err)
	}
	if answered != len(sigs) {
		t.Fatalf("%d answered", answered)
	}
	if d := time.Since(t0); d > 30*time.Second { //qenv:ok ~0.2 s; generous for -race
		t.Fatalf("took %v", d)
	}
	t.Logf("20000 waiting requests: %v", time.Since(t0)) //qenv:ok the real cost
}

// A Signal wait that timed out leaves no trace: a later Notify does not
// cut short the task's next wait.
func TestSignalTimeoutIsForgotten(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	var sig qenv.Signal
	var slept time.Duration
	s.Spawn("waiter", func(ctx context.Context) {
		if sig.Wait(ctx, s.Now().Add(time.Second)) {
			t.Error("woken before any Notify")
		}
		start := s.Elapsed()
		qenv.Sleep(ctx, 10*time.Second)
		slept = s.Elapsed() - start
	})
	s.Spawn("notifier", func(ctx context.Context) {
		qenv.Sleep(ctx, 5*time.Second)
		sig.Notify()
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if slept != 10*time.Second {
		t.Errorf("slept %v, want 10s: a stale Signal entry woke the sleep", slept)
	}
}

// A service with Capacity queues calls for its workers, rejects calls
// over its queue, and keeps serving calls whose caller gave up, unless it
// sheds them.
func TestServiceCapacity(t *testing.T) {
	for _, shed := range []bool{false, true} {
		s := qenv.New(qenv.Nominal{})
		svc := qenv.NewService(s, "api", nil, func(ctx context.Context, n int) (int, error) { return n, nil })
		svc.Capacity = &qenv.Capacity{Workers: 1, Queue: 2, Time: func() time.Duration { return time.Second }, Shed: shed}
		var ok, timedOut, rejected int
		for i := 0; i < 4; i++ {
			s.Spawn("client", func(ctx context.Context) {
				_, err := svc.Call(ctx, i, 1500*time.Millisecond)
				switch {
				case err == nil:
					ok++
				case errors.Is(err, qenv.ErrTimeout):
					timedOut++
				case errors.Is(err, qenv.ErrOverloaded):
					rejected++
				}
			})
		}
		if err := s.Run(10 * time.Second); err != nil {
			t.Fatal(err)
		}
		s.Close()
		// one served, two queued (both time out: 2 s and 3 s of work), one rejected
		if ok != 1 || timedOut != 2 || rejected != 1 {
			t.Errorf("shed=%v: %d ok, %d timed out, %d rejected; want 1, 2, 1", shed, ok, timedOut, rejected)
		}
		// the timed-out calls are still served (wasted work), unless their
		// deadline passed before they started: the third call's, at 2 s
		wantCalls := 3
		if shed {
			wantCalls = 2
		}
		if svc.Calls != wantCalls || (shed && svc.Shed != 1) {
			t.Errorf("shed=%v: %d calls served, %d shed; want %d served", shed, svc.Calls, svc.Shed, wantCalls)
		}
	}
}

// With NemesisEvery, a nemesis is a choice once per period of virtual
// time, however many steps the system takes.
func TestNemesisEvery(t *testing.T) {
	for _, busy := range []int{1, 50} {
		c := qenv.NewSeeded(1)
		c.Rates = qenv.Rates{Kinds: map[string]float64{"nemesis": 1}}
		s := qenv.New(c)
		s.NemesisEvery = time.Second
		crashes := 0
		s.OnCrash(func(*qenv.Process) { crashes++ })
		s.Process("worker", true, func(ctx context.Context) {
			for {
				for range busy {
					qenv.Yield(ctx)
				}
				qenv.Sleep(ctx, 10*time.Millisecond)
			}
		})
		if err := s.Run(10 * time.Second); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if crashes < 8 || crashes > 10 {
			t.Errorf("%d yields per tick: %d crashes in 10 s, want about one a second", busy, crashes)
		}
	}
}

// Steps recorded with context.Background() land in the run whose
// goroutine records them, a task's or the simulation's creator's, even
// with simulations running in parallel.
func TestBackgroundStepsFindTheirRun(t *testing.T) {
	var wg sync.WaitGroup
	sinks := make([]*qobs.MemorySink, 8)
	for i := range sinks {
		sinks[i] = &qobs.MemorySink{}
		wg.Add(1)
		go func() { //qenv:ok simulations in parallel, each from its own goroutine
			defer wg.Done()
			ctx := qobs.WithRecorder(context.Background(), qobs.NewRecorder(sinks[i]))
			s := qenv.New(qenv.WithContext(qenv.Nominal{}, ctx))
			defer s.Close()
			for range 3 {
				s.Spawn("task", func(ctx context.Context) {
					qenv.Sleep(ctx, time.Millisecond)
					qobs.Record(context.Background(), "work", nil, "run", i) //qenv:ok tests the goroutine fallback
					qenv.Go(ctx, "child", func(context.Context) {
						qobs.Record(context.Background(), "child", nil, "run", i) //qenv:ok and a child's
					})
				})
			}
			if err := s.Run(time.Second); err != nil {
				t.Error(err)
			}
			qobs.Record(context.Background(), "settle", nil, "run", i) //qenv:ok the creator's goroutine
		}()
	}
	wg.Wait()
	for i, m := range sinks {
		steps := m.Steps()
		if len(steps) != 7 {
			t.Errorf("run %d: %d steps, want 7: %v", i, len(steps), steps)
		}
		for _, s := range steps {
			if s.Args["run"] != int64(i) {
				t.Errorf("run %d got a step of run %v", i, s.Args["run"])
			}
		}
	}
}

// While a run is strict, a step with nowhere to go is logged with what to
// do. It once panicked, on a goroutine no run owns, which killed the test
// binary instead of failing the run.
func TestStrictStepWithNowhereToGo(t *testing.T) {
	release := qobs.Strict()
	defer release()
	mark, _ := qobs.Escaped(0)
	qobs.Record(context.Background(), "lost", nil) //qenv:ok a step with nowhere to go
	n, msgs := qobs.Escaped(mark)
	if n != mark+1 || len(msgs) != 1 || !strings.Contains(msgs[0], "qenv.Go") {
		t.Fatalf("got %d escaped since %d: %v; want one naming qenv.Go", n, mark, msgs)
	}
}

// A task killed by a crash stays frozen, and unwinds only when the
// simulation is closed. A step one of its deferred functions records then
// (as //quint:action advice does on return) describes work that never
// completed: it goes to the recorder's Interrupted list, not the run.
func TestKilledTasksRecordNothing(t *testing.T) {
	mem := &qobs.MemorySink{}
	rec := qobs.NewRecorder(mem)
	s := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(context.Background(), rec)))
	cleaned := false
	p := s.Process("writer", true, func(ctx context.Context) {
		if qenv.ProcessOf(ctx).Incarnation > 1 {
			return
		}
		func() {
			defer qobs.Record(ctx, "write", nil) // recorded on return
			defer func() { cleaned = true }()
			qenv.Sleep(ctx, time.Hour) // crashed before it returns
		}()
	})
	s.Spawn("nemesis", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Second)
		p.Crash()
		qobs.Record(ctx, "crashed", nil)
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if cleaned {
		t.Fatal("a crashed task's deferred functions ran during the run: it should stay frozen")
	}
	s.Close() // unwinds the frozen task
	if steps := mem.Steps(); len(steps) != 1 || steps[0].Action != "crashed" {
		t.Fatalf("steps %v, want only the nemesis's", steps)
	}
	if in := rec.Interrupted(); len(in) != 1 || in[0].Action != "write" {
		t.Fatalf("interrupted %v, want the write", in)
	}
}

// A task that crashes its own process stops there, as if the process died
// under it: nothing after the Crash runs, until the simulation is closed.
func TestSelfCrashStopsAtOnce(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	after := false
	s.Process("suicidal", true, func(ctx context.Context) {
		if qenv.ProcessOf(ctx).Incarnation > 1 {
			return
		}
		qenv.ProcessOf(ctx).Crash()
		after = true
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if after {
		t.Fatal("the task ran on after crashing its own process")
	}
}

// Sleep, Go and Wait called with a context that carries no task (as a
// library or woven code might) from a task's goroutine still act in the
// simulation: virtual time passes, not real time.
func TestBackgroundContextInTask(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	var slept time.Duration
	s.Spawn("task", func(context.Context) {
		bg := context.Background()
		start := s.Elapsed()
		qenv.Sleep(bg, time.Hour)
		qenv.Go(bg, "child", func(ctx context.Context) { qenv.Sleep(ctx, time.Minute) })
		slept = s.Elapsed() - start
	})
	t0 := time.Now() //qenv:ok real time, to show the sleeps were virtual
	if err := s.Run(2 * time.Hour); err != nil {
		t.Fatal(err)
	}
	if slept != time.Hour || time.Since(t0) > 5*time.Second { //qenv:ok real time, to show the sleeps were virtual
		t.Fatalf("slept %v of virtual time in %v of real time", slept, time.Since(t0)) //qenv:ok real time
	}
}
