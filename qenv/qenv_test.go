package qenv_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/chucklehead-dev/gotogether/internal/hooks"
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
	q := qenv.NewQueue(s, "jobs", nil)
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
	if testing.Short() {
		t.Skip("timing: 20000 tasks in real time (and woven mutants make every step slower)")
	}
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	sigs := make([]*qenv.Signal, 20000)
	answered := 0
	for i := range sigs {
		sigs[i] = &qenv.Signal{}
		s.Spawn("request", func(ctx context.Context) {
			if sigs[i].Wait(ctx, time.Hour) {
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
		if sig.Wait(ctx, time.Second) {
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

// A negative wait has passed already: it times out at once, in a
// simulation and outside one, instead of waiting without limit.
func TestSignalNegativeWait(t *testing.T) {
	var sig qenv.Signal
	if sig.Wait(context.Background(), -time.Second) {
		t.Error("woken outside a simulation with no Notify")
	}
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	var at time.Duration = -1
	s.Spawn("waiter", func(ctx context.Context) {
		if sig.Wait(ctx, -time.Second) {
			t.Error("woken with no Notify")
		}
		at = s.Elapsed()
	})
	if err := s.Run(0); err != nil {
		t.Fatal(err)
	}
	if at != 0 {
		t.Errorf("returned at %v, want at once", at)
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
	s := qenv.New(qenv.Nominal{}) // this goroutine's: the advice is qenv's for a simulation's goroutine
	defer s.Close()
	release := hooks.Strict()
	defer release()
	mark, _ := hooks.Escaped(0)
	qobs.Record(context.Background(), "lost", nil) //qenv:ok a step with nowhere to go
	n, msgs := hooks.Escaped(mark)
	if n != mark+1 || len(msgs) != 1 || !strings.Contains(msgs[0], "qenv.Go") {
		t.Fatalf("got %d escaped since %d: %v; want one naming qenv.Go", n, mark, msgs)
	}
}

// The advice follows the escaped step's goroutine, not the process: one
// no simulation started is not told to use qenv.Go though a simulation
// is open elsewhere (a test of a real server beside an exploration).
func TestEscapeAdviceFollowsTheGoroutine(t *testing.T) {
	opened, done := make(chan struct{}), make(chan struct{})
	go func() { //qenv:ok another test's simulation, open meanwhile
		s := qenv.New(qenv.Nominal{})
		close(opened)
		<-done
		s.Close()
	}()
	<-opened
	defer close(done)
	release := hooks.Strict()
	defer release()
	mark, _ := hooks.Escaped(0)
	recorded := make(chan struct{})
	go func() { //qenv:ok a goroutine of no simulation
		defer close(recorded)
		qobs.Record(context.Background(), "lost", nil)
	}()
	<-recorded
	if _, msgs := hooks.Escaped(mark); len(msgs) != 1 || strings.Contains(msgs[0], "qenv.Go") || !strings.Contains(msgs[0], "qobs.WithRecorder(ctx, rec)") {
		t.Fatalf("escaped: %q; want the advice of a goroutine outside any simulation", msgs)
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

// A crash of a Capacity service's process kills the requests in service
// and waiting with it; the restarted service has every worker free (a
// killed handler's deferred release never ran, and leaked them).
func TestCapacityAfterCrash(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	s.OnCrash(func(p *qenv.Process) { p.Restart() })
	server := s.Process("server", true, func(context.Context) {})
	svc := qenv.NewService(s, "slow", server, func(ctx context.Context, n int) (int, error) { return n, nil })
	svc.Capacity = &qenv.Capacity{Workers: 1, Queue: 1, Time: func() time.Duration { return 10 * time.Second }}
	var second error = errors.New("not called")
	s.Spawn("client", func(ctx context.Context) {
		qenv.Go(ctx, "first", func(ctx context.Context) { _, _ = svc.Call(ctx, 1, time.Minute) })
		qenv.Sleep(ctx, time.Second)
		server.Crash() // the first request dies in service
		qenv.Sleep(ctx, time.Second)
		_, second = svc.Call(ctx, 2, time.Minute)
	})
	if err := s.Run(5 * time.Minute); err != nil {
		t.Fatal(err)
	}
	if second != nil || svc.InSystem() != 0 {
		t.Errorf("after the crash: the second call %v, %d requests in the system", second, svc.InSystem())
	}
}

// last schedules the last task offered and duplicates the second request:
// its duplicate then runs before the copy it duplicates.
type last struct{ requests int }

func (l *last) Choose(kind string, n int) int {
	switch {
	case strings.HasSuffix(kind, ".request"):
		if l.requests++; l.requests == 2 {
			return 2 // duplicate
		}
		return 0
	case strings.HasPrefix(kind, "fault."):
		return 0
	}
	return n - 1
}

// ErrOverloaded says the request was not processed: a duplicate that got
// the queue's last place, ahead of its first copy, is not processed after
// that copy was refused, as it once was.
func TestOverloadedMeansNotProcessed(t *testing.T) {
	s := qenv.New(&last{})
	defer s.Close()
	var handled []int
	svc := qenv.NewService(s, "slow", nil, func(ctx context.Context, n int) (int, error) { handled = append(handled, n); return n, nil })
	svc.Capacity = &qenv.Capacity{Workers: 1, Queue: 1, Time: func() time.Duration { return time.Second }}
	var errs []error
	s.Spawn("client", func(ctx context.Context) {
		qenv.Go(ctx, "busy", func(ctx context.Context) { _, _ = svc.Call(ctx, 1, time.Minute) })
		qenv.Sleep(ctx, 100*time.Millisecond) // 1 in service: one place left in the queue
		_, err := svc.Call(ctx, 2, time.Minute)
		errs = append(errs, err)
	})
	if err := s.Run(5 * time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, n := range handled {
		if n == 2 && errors.Is(errs[0], qenv.ErrOverloaded) {
			t.Fatalf("request 2 was processed (handled %v) and its caller told it was rejected", handled)
		}
	}
	t.Logf("handled %v, the call: %v", handled, errs)
}

// Two doubles of one kind and name in a simulation would share their
// fault kinds: the second panics, naming the fix. Doubles of other kinds
// may share a name.
func TestDuplicateDoubleName(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	qenv.NewQueue(s, "orders", nil)
	qenv.NewStore(s, "orders", qenv.Serializable) // another kind: fine
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), `a second Queue named "orders"`) {
			t.Errorf("recovered %v, want the duplicate named", r)
		}
	}()
	qenv.NewQueue(s, "orders", nil)
}

// faultAt makes option v of fault kind kind, every choice else nominal.
type faultAt struct {
	kind string
	v    int
}

func (f faultAt) Choose(kind string, n int) int {
	if kind == "fault."+f.kind && f.v < n {
		return f.v
	}
	return 0
}

// A late request is processed after its caller timed out, and its
// response reaches no one, as a delayed packet's: just after the timeout
// (late), or a timeout after it, after a retry that backed off
// (veryLate); lateDuplicate delivers the request at once and such a copy;
// a slow one arrives at half the timeout, and its response in time.
func TestLateRequests(t *testing.T) {
	for _, c := range []struct {
		fault      string
		v          int
		err        error
		atTimeout  int // handled by the caller's timeout
		afterwards int // handled in all
	}{
		{"late", 1, qenv.ErrTimeout, 0, 1},
		{"lateDuplicate", 2, nil, 1, 2},
		{"veryLate", 3, qenv.ErrTimeout, 0, 1},
		{"slow", 4, nil, 1, 1},
	} {
		s := qenv.New(faultAt{"rpc.api.delay", c.v})
		var at []time.Duration
		svc := qenv.NewService(s, "api", nil, func(ctx context.Context, n int) (int, error) {
			at = append(at, s.Elapsed())
			return n, nil
		})
		var err error
		var handled int
		s.Spawn("client", func(ctx context.Context) {
			_, err = svc.Call(ctx, 1, time.Second)
			handled = svc.Calls
		})
		if runErr := s.Run(time.Minute); runErr != nil {
			t.Fatal(runErr)
		}
		s.Close()
		if !errors.Is(err, c.err) || handled != c.atTimeout || svc.Calls != c.afterwards {
			t.Errorf("%s: err %v, handled %d by the timeout and %d in all (at %v); want %v, %d, %d",
				c.fault, err, handled, svc.Calls, at, c.err, c.atTimeout, c.afterwards)
		}
		wantAt := map[string]time.Duration{"late": time.Second + time.Millisecond, "lateDuplicate": 2 * time.Second,
			"veryLate": 2 * time.Second, "slow": time.Second / 2}[c.fault]
		if n := len(at); n == 0 || at[n-1] != wantAt {
			t.Errorf("%s: the delayed copy was handled at %v, want %v", c.fault, at, wantAt)
		}
	}
}

// A paused process's tasks freeze at their next scheduling point and go
// on after the pause, unaware: a worker ticking every second ticks once
// late, by the pause. A slow one is alive, but each scheduling point
// costs more time while it is slow.
func TestStalls(t *testing.T) {
	for _, c := range []struct {
		nemesis string
		gap     time.Duration // the longest between ticks
		end     time.Duration // the last tick
	}{
		{"nemesis.pause.worker", 10 * time.Second, 14 * time.Second}, // 1s ticks, one 10s late
		{"nemesis.slow.worker", 2 * time.Second, 6 * time.Second},    // 1s more per point while slow (3s)
	} {
		var ticks []time.Duration
		sc := &qenv.Script{Faults: []qenv.Scripted{{Kind: c.nemesis, After: 1}}, Progress: func() int { return len(ticks) }}
		s := qenv.New(sc)
		s.Check = true
		s.Stalls = qenv.Stalls{Pause: 10 * time.Second, Slow: time.Second, SlowFor: 3 * time.Second}
		s.Process("worker", true, func(ctx context.Context) {
			for range 5 {
				ticks = append(ticks, s.Elapsed())
				qenv.Sleep(ctx, time.Second)
			}
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if left := sc.Remaining(); len(left) > 0 {
			t.Fatalf("%s: never fired: %v", c.nemesis, left)
		}
		var gap time.Duration
		for i := 1; i < len(ticks); i++ {
			gap = max(gap, ticks[i]-ticks[i-1])
		}
		if len(ticks) != 5 || gap < c.gap || ticks[4] > c.end {
			t.Errorf("%s: ticks %v; want 5, the longest gap at least %v, the last by %v", c.nemesis, ticks, c.gap, c.end)
		}
	}
}

// A stall delays a task's wake, never changes it: a notified Signal.Wait
// still reports the Notify under a slowdown, and a lock built on a
// predicate wait keeps one holder at a time through pauses and
// slowdowns (a woken waiter checks its predicate again when its process
// goes on; one stalled after its wake was decided once ran on a stale
// one).
func TestStallsKeepWaitsTrue(t *testing.T) {
	for seed := uint64(0); seed < 50; seed++ {
		sd := qenv.NewSeeded(seed)
		sd.Rates = qenv.NoFaults.Set("nemesis.pause", 0.3).Set("nemesis.slow", 0.3)
		s := qenv.New(sd)
		s.Check = true
		s.Stalls = qenv.Stalls{Pause: 2 * time.Second, Slow: 300 * time.Millisecond, SlowFor: 3 * time.Second}
		var sig qenv.Signal
		var notified []bool
		holders, most, locked, entered := 0, 0, false, 0
		for i := range 3 {
			s.Process(fmt.Sprintf("p%d", i), false, func(ctx context.Context) {
				for range 3 {
					qenv.Wait(ctx, func() bool { return !locked })
					locked = true
					holders++
					most = max(most, holders)
					qenv.Sleep(ctx, 100*time.Millisecond)
					holders--
					locked = false
				}
				entered++                                     // and in Wait at once: no scheduling point between
				notified = append(notified, sig.Wait(ctx, 0)) // no limit: the Notify comes once all three wait
			})
		}
		s.Spawn("notifier", func(ctx context.Context) {
			qenv.Wait(ctx, func() bool { return entered == 3 }) // every waiter registered: each is notified
			sig.Notify()
		})
		if err := s.Run(time.Hour); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if most > 1 {
			t.Errorf("seed %d: %d holders of the lock at once", seed, most)
		}
		for i, n := range notified {
			if !n {
				t.Errorf("seed %d: wait %d was notified but reported a timeout", seed, i)
			}
		}
	}
}

// A delay is the network's, in true time: a late request arrives after its
// caller's deadline whatever the service's clock does (a fast one once
// handled it before).
func TestLateRequestsIgnoreDrift(t *testing.T) {
	for _, drift := range []int{1, 2} { // fast, slow
		s := qenv.New(byKind{"fault.rpc.api.delay": {1}, "fault.clock.drift": {drift, drift, drift}})
		s.Clocks = qenv.Clocks{Drift: 0.25}
		var at time.Duration
		svc := qenv.NewService(s, "api", nil, func(ctx context.Context, n int) (int, error) {
			at = s.Elapsed()
			return n, nil
		})
		s.Spawn("client", func(ctx context.Context) { _, _ = svc.Call(ctx, 1, time.Second) })
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if at != time.Second+time.Millisecond {
			t.Errorf("drift option %d: the late request was handled at %v, want just after the deadline", drift, at)
		}
	}
}

// Settle says whether the system settled: a client still retrying when
// its time is up, or one waiting forever on a reply while a daemon's
// periodic timer keeps time moving (which once hid such a stall), is
// Unsettled, naming the task; a daemon's own loop and a finished client
// are quiet.
func TestSettleSaysWhetherItSettled(t *testing.T) {
	for _, c := range []struct {
		name    string
		client  func(ctx context.Context, sig *qenv.Signal)
		settled bool
	}{
		{"finishes", func(ctx context.Context, _ *qenv.Signal) { qenv.Sleep(ctx, time.Second) }, true},
		{"retries forever", func(ctx context.Context, _ *qenv.Signal) {
			for {
				qenv.Sleep(ctx, time.Second)
			}
		}, false},
		{"waits forever", func(ctx context.Context, sig *qenv.Signal) { sig.Wait(ctx, 0) }, false},
	} {
		s := qenv.New(qenv.Nominal{})
		var sig qenv.Signal
		s.Spawn("ticker", func(ctx context.Context) { // a daemon's periodic timer
			qenv.Daemon(ctx)
			for {
				qenv.Sleep(ctx, 100*time.Millisecond)
			}
		})
		s.Spawn("client", func(ctx context.Context) { c.client(ctx, &sig) })
		err := s.Settle(10 * time.Second)
		s.Close()
		var u *qenv.Unsettled
		if c.settled != (err == nil) || !c.settled && (!errors.As(err, &u) || !errors.Is(err, qenv.ErrUnsettled) || len(u.Tasks) != 1 || !strings.HasPrefix(u.Tasks[0], "client: waiting")) {
			t.Errorf("%s: Settle = %v", c.name, err)
		}
	}
}

// Faults stay off after a settle, so a probe after it runs on the settled
// system, until ResumeFaults starts a faulty phase again: a run of phases
// was once fault-free after its first settle, whatever its Rates.
func TestFaultsAfterSettle(t *testing.T) {
	c := qenv.NewSeeded(1)
	c.Rates = qenv.DefaultRates.Set("rpc", 0.9)
	s := qenv.New(c)
	defer s.Close()
	svc := qenv.NewService(s, "echo", nil, func(ctx context.Context, x int) (int, error) { return x, nil })
	phase := func() int {
		timeouts := 0
		s.Spawn("client", func(ctx context.Context) {
			for i := range 50 {
				if _, err := svc.Call(ctx, i, time.Millisecond); errors.Is(err, qenv.ErrTimeout) {
					timeouts++
				}
			}
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		return timeouts
	}
	if n := phase(); n == 0 {
		t.Fatal("no fault before the settle")
	}
	if err := s.Settle(time.Second); err != nil {
		t.Fatal(err)
	}
	if n := phase(); n != 0 {
		t.Errorf("%d faults after the settle: a probe would not run on the settled system", n)
	}
	s.ResumeFaults()
	if n := phase(); n == 0 {
		t.Error("no fault after ResumeFaults")
	}
}

// A settle takes runnable tasks in turn: a task that yields until another
// sets a flag (a spin wait, woven code's runtime.Gosched) once kept the
// lowest id first forever, so the setter never ran and the settle ran out
// of steps instead of settling.
func TestSettleIsFair(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	done := false
	s.Spawn("spinner", func(ctx context.Context) {
		for !done {
			qenv.Yield(ctx)
		}
	})
	s.Spawn("setter", func(ctx context.Context) { done = true })
	if err := s.Settle(time.Second); err != nil {
		t.Fatalf("Settle = %v", err)
	}
	if !done {
		t.Fatal("the setter never ran")
	}
}

// A core settle leaves the core alone and goes on faulting the rest: c
// crashes and its service drops requests while a and b, and a's service,
// see no fault; a plain settle faults nothing. The client, of no process,
// is the core's work: it finishes, so the settle settles.
func TestSettleCore(t *testing.T) {
	for _, core := range []bool{true, false} {
		log := &qenv.ChoiceLog{C: qenv.NewSeeded(3)}
		log.C.(*qenv.Seeded).Rates = qenv.Rates{Kinds: map[string]float64{"nemesis": 1, "rpc": 0.5}}
		s := qenv.New(log)
		s.NemesisEvery = 100 * time.Millisecond
		procs := map[string]*qenv.Process{}
		for _, n := range []string{"a", "b", "c"} {
			procs[n] = s.Process(n, true, func(ctx context.Context) {
				qenv.Daemon(ctx)
				for {
					qenv.Sleep(ctx, 10*time.Millisecond)
				}
			})
		}
		echo := func(_ context.Context, v int) (int, error) { return v, nil }
		svcA := qenv.NewService(s, "sa", procs["a"], echo)
		svcC := qenv.NewService(s, "sc", procs["c"], echo)
		s.Spawn("client", func(ctx context.Context) {
			for i := range 50 {
				svcA.Call(ctx, i, time.Second)
				svcC.Call(ctx, i, time.Second)
			}
		})
		crashes := map[string]int{}
		s.OnCrash(func(p *qenv.Process) { crashes[p.Name]++ })
		var err error
		if core {
			err = s.SettleCore(time.Minute, procs["a"], procs["b"])
		} else {
			err = s.Settle(time.Minute)
		}
		s.Close()
		if err != nil {
			t.Fatalf("core %v: %v", core, err)
		}
		faulted := map[string]int{}
		for _, c := range log.Choices {
			if c.Value != 0 && strings.HasPrefix(c.Kind, "fault.rpc.") {
				faulted[strings.Split(c.Kind, ".")[2]]++
			}
		}
		if core && (crashes["c"] == 0 || faulted["sc"] == 0 || crashes["a"]+crashes["b"] > 0 || faulted["sa"] > 0) {
			t.Errorf("core settle: crashes %v, faulted requests %v: want c's only", crashes, faulted)
		}
		if !core && (len(crashes) > 0 || len(faulted) > 0) {
			t.Errorf("settle: crashes %v, faulted requests %v: want none", crashes, faulted)
		}
	}
}

// A core settle waits for the core's work, not the rest's: a process
// outside the core retrying forever leaves it settled, one inside does not.
func TestSettleCoreWaitsForTheCore(t *testing.T) {
	for _, inCore := range []bool{false, true} {
		s := qenv.New(qenv.Nominal{})
		retry := s.Process("retrier", true, func(ctx context.Context) {
			for {
				qenv.Sleep(ctx, time.Second)
			}
		})
		other := s.Process("other", true, func(ctx context.Context) { qenv.Daemon(ctx); qenv.Sleep(ctx, time.Hour) })
		core := []*qenv.Process{other}
		if inCore {
			core = append(core, retry)
		}
		err := s.SettleCore(10*time.Second, core...)
		s.Close()
		if inCore != errors.Is(err, qenv.ErrUnsettled) {
			t.Errorf("retrier in the core %v: SettleCore = %v", inCore, err)
		}
	}
}
