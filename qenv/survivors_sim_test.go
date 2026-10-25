package qenv_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// svOnce makes option v of kind the first time it is asked, every other
// choice 0.
type svOnce struct {
	kind string
	v    int
	done bool
}

func (o *svOnce) Choose(kind string, n int) int {
	if kind == o.kind && !o.done && o.v < n {
		o.done = true
		return o.v
	}
	return 0
}

// svRecover runs f and returns what it panicked with, as a string ("" if
// it did not).
func svRecover(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	f()
	return ""
}

// svYields is a process's start that yields n times: steps at which the
// scheduler considers its nemeses.
func svYields(n int) func(ctx context.Context) {
	return func(ctx context.Context) {
		for range n {
			qenv.Yield(ctx)
		}
	}
}

// Sim.Log gets a line for every fault, crash, upgrade, stall and task
// panic: what a failing run's log shows of how it got there. Each case
// makes one happen and looks for its line.
func TestSimLogsWhatHappens(t *testing.T) {
	for _, c := range []struct {
		name    string
		chooser qenv.Chooser
		stalls  qenv.Stalls
		run     func(t *testing.T, s *qenv.Sim)
		want    string
	}{
		{"fault", faultAt{"sv.log", 1}, qenv.Stalls{}, func(t *testing.T, s *qenv.Sim) {
			s.Spawn("chooser", func(ctx context.Context) { s.Choose("fault.sv.log", 2) })
			if err := s.Run(time.Second); err != nil {
				t.Fatal(err)
			}
		}, "fault.sv.log=1"},
		{"panic", qenv.Nominal{}, qenv.Stalls{}, func(t *testing.T, s *qenv.Sim) {
			s.Spawn("bomber", func(ctx context.Context) { panic("boom") })
			if msg := svRecover(func() { _ = s.Run(time.Second) }); msg != "boom" {
				t.Fatalf("Run re-raised %q, want the task's own boom", msg)
			}
		}, "task bomber panicked: boom"},
		{"crash", qenv.Nominal{}, qenv.Stalls{}, func(t *testing.T, s *qenv.Sim) {
			p := s.Process("p", false, svYields(1))
			p.Crash()
		}, "crash p#1"},
		{"upgrade", qenv.Nominal{}, qenv.Stalls{}, func(t *testing.T, s *qenv.Sim) {
			p := s.Process("p", false, svYields(1))
			p.Upgrade("v2", svYields(1))
		}, `upgrade p to "v2"`},
		{"pause", &qenv.Script{Faults: []qenv.Scripted{{Kind: "nemesis.pause.p"}}}, qenv.Stalls{Pause: time.Second}, func(t *testing.T, s *qenv.Sim) {
			s.Process("p", false, svYields(3))
			if err := s.Run(time.Minute); err != nil {
				t.Fatal(err)
			}
		}, "pause p#1 for 1s"},
		{"slow", &qenv.Script{Faults: []qenv.Scripted{{Kind: "nemesis.slow.p"}}}, qenv.Stalls{Slow: time.Millisecond, SlowFor: time.Second}, func(t *testing.T, s *qenv.Sim) {
			s.Process("p", false, svYields(3))
			if err := s.Run(time.Minute); err != nil {
				t.Fatal(err)
			}
		}, "slow p#1 for 1s"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := qenv.New(c.chooser)
			defer s.Close()
			s.Stalls = c.stalls
			var lines []string
			s.Log = func(l string) { lines = append(lines, l) }
			c.run(t, s)
			if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, c.want) }) {
				t.Errorf("log %q: no line with %q", lines, c.want)
			}
		})
	}
}

// A simulation offers exactly the nemeses its configuration makes: a
// crashable process's crash, a stall only when its sizes are set (a slow
// one needs both Slow and SlowFor), a clock's jumps both ways, a test's
// own Nemesis, and a partition per pair named in name order however the
// pair was given, once. A nemesis missing here is a fault no run, Script
// or Rates key can reach.
func TestSimOffersTheNemesesItsConfigurationMakes(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(s *qenv.Sim)
		want  []string
	}{
		{"none", func(s *qenv.Sim) { s.Process("p", false, svYields(3)) }, nil},
		{"crash", func(s *qenv.Sim) { s.Process("p", true, svYields(3)) }, []string{"crash.p"}},
		{"pause", func(s *qenv.Sim) {
			s.Stalls = qenv.Stalls{Pause: time.Second}
			s.Process("p", false, svYields(3))
		}, []string{"pause.p"}},
		{"slow", func(s *qenv.Sim) {
			s.Stalls = qenv.Stalls{Slow: time.Millisecond, SlowFor: time.Second}
			s.Process("p", false, svYields(3))
		}, []string{"slow.p"}},
		{"slow without SlowFor", func(s *qenv.Sim) {
			s.Stalls = qenv.Stalls{Slow: time.Millisecond}
			s.Process("p", false, svYields(3))
		}, nil},
		{"SlowFor without Slow", func(s *qenv.Sim) {
			s.Stalls = qenv.Stalls{SlowFor: time.Second}
			s.Process("p", false, svYields(3))
		}, nil},
		{"jumps", func(s *qenv.Sim) {
			s.Clocks = qenv.Clocks{Jump: time.Second}
			s.Process("p", false, svYields(3))
		}, []string{"jump.back.p", "jump.forward.p"}},
		{"the test's own", func(s *qenv.Sim) {
			s.Nemesis("svcustom.x", func() bool { return true }, func() {})
			s.Spawn("driver", svYields(3))
		}, []string{"svcustom.x"}},
		{"partition", func(s *qenv.Sim) {
			b := s.Process("b", false, svYields(3))
			a := s.Process("a", false, svYields(3))
			s.Net().PartitionNemesis(b, a)
			s.Net().PartitionNemesis(a, b) // the same pair: still one
		}, []string{"partition.a.b"}},
	} {
		log := &qenv.ChoiceLog{C: qenv.Nominal{}}
		s := qenv.New(log)
		c.setup(s)
		if err := s.Run(time.Minute); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		s.Close()
		got := slices.Sorted(slices.Values(log.Nemeses))
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: offered %v, want %v", c.name, got, c.want)
		}
	}
}

// A partition the nemesis makes heals after the network's CutLength (by
// default DefaultPartitionLength), or sooner by its heal nemesis, which
// may heal it even when a cut made by hand between the same pair was
// healed before: Heal ends a hand cut's claim on the pair.
func TestPartitionNemesisHeals(t *testing.T) {
	for _, c := range []struct {
		name   string
		cut    time.Duration
		byHand bool
		faults []qenv.Scripted
		want   time.Duration
	}{
		{"after CutLength", 100 * time.Millisecond, false, []qenv.Scripted{{Kind: "nemesis.partition.a.b"}}, 100 * time.Millisecond},
		{"after the default length", 0, false, []qenv.Scripted{{Kind: "nemesis.partition.a.b"}}, qenv.DefaultPartitionLength},
		{"by its heal nemesis after a hand cut healed", 0, true,
			[]qenv.Scripted{{Kind: "nemesis.partition.a.b"}, {Kind: "nemesis.heal.a.b", Follows: []int{0}}}, 0},
	} {
		sc := &qenv.Script{Faults: c.faults}
		s := qenv.New(sc)
		a := s.Process("a", false, svYields(5))
		b := s.Process("b", false, svYields(5))
		n := s.Net()
		n.CutLength = c.cut
		n.PartitionNemesis(a, b)
		if c.byHand {
			n.Partition(a, b)
			n.Heal(a, b)
		}
		var heals []time.Duration
		n.OnHeal(func(_, _ *qenv.Process) { heals = append(heals, s.Elapsed()) })
		if err := s.Run(time.Minute); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		s.Close()
		if !slices.Equal(heals, []time.Duration{c.want}) || len(sc.Remaining()) != 0 {
			t.Errorf("%s: healed at %v (scripted faults left: %v), want once at %v", c.name, heals, sc.Remaining(), c.want)
		}
	}
}

// Settle heals every cut and runs their OnHeal hooks in the pairs' order
// (by name), however and in whatever order they were cut: a test that
// records heals gets the same trace from the same run.
func TestSettleHealsEveryCutInOrder(t *testing.T) {
	for range 10 { // the map of cuts iterates in a new order each time
		s := qenv.New(qenv.Nominal{})
		ps := map[string]*qenv.Process{}
		for _, name := range []string{"a", "b", "c", "d"} {
			ps[name] = s.Process(name, false, func(context.Context) {})
		}
		n := s.Net()
		for _, pair := range [][2]string{{"d", "a"}, {"c", "b"}, {"c", "a"}, {"b", "d"}} {
			n.Partition(ps[pair[0]], ps[pair[1]])
		}
		var heals []string
		n.OnHeal(func(x, y *qenv.Process) {
			names := []string{x.Name, y.Name}
			slices.Sort(names)
			heals = append(heals, strings.Join(names, "-"))
		})
		if err := s.Settle(time.Second); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if want := []string{"a-c", "a-d", "b-c", "b-d"}; !slices.Equal(heals, want) {
			t.Fatalf("healed %v, want %v", heals, want)
		}
	}
}

// svTaskOver picks a task one past the last runnable.
type svTaskOver struct{ qenv.Nominal }

func (svTaskOver) ChooseTask(runnable []int) int { return len(runnable) }

// svOver returns n, one past the last option, for kind.
type svOver struct{ kind string }

func (o svOver) Choose(kind string, n int) int {
	if kind == o.kind {
		return n
	}
	return 0
}

// A chooser that answers out of range is a broken chooser, and the
// scheduler says so, naming the choice, rather than running on with an
// option that does not exist (an index past the options, a task that is
// not runnable).
func TestSchedulerRefusesAnOptionOutOfRange(t *testing.T) {
	for _, c := range []struct {
		name    string
		chooser qenv.Chooser
		want    string
	}{
		{"a choice", svOver{"fault.sv.over"}, "chooser returned 2 for fault.sv.over with 2 options"},
		{"a task", svTaskOver{}, "ChooseTask returned 2 of 2 runnable tasks"},
	} {
		s := qenv.New(c.chooser)
		got := svRecover(func() {
			s.Spawn("one", func(ctx context.Context) {
				if v := s.Choose("fault.sv.over", 2); v != 0 {
					panic(fmt.Sprintf("Choose made option %d of 2", v))
				}
			})
			s.Spawn("two", svYields(1))
			_ = s.Run(time.Second)
		})
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: panicked with %q, want %q", c.name, got, c.want)
		}
		s.Close()
	}
}

// A fault kind no one declared but a double chose is a kind all the same:
// a Script naming it is not misnamed, and makes its fault.
func TestAChosenUndeclaredKindIsScriptable(t *testing.T) {
	const kind = "svundeclared.kind"
	sc := &qenv.Script{Faults: []qenv.Scripted{{Kind: kind, Option: 1}}}
	s := qenv.New(sc)
	defer s.Close()
	var got int
	s.Spawn("double", func(ctx context.Context) { got = s.Choose("fault."+kind, 2) })
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if m := sc.Misnamed(); got != 1 || len(m) != 0 {
		t.Errorf("chose %d, misnamed %q: want the scripted fault, and its kind known", got, m)
	}
}

// From finds the simulation of a task's context, and of a task's
// goroutine with a context that carries none (a library's
// context.Background()); outside any, nil.
func TestFromFindsTheTasksSimulation(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	var fromCtx, fromGoroutine *qenv.Sim
	s.Spawn("task", func(ctx context.Context) {
		fromCtx, fromGoroutine = qenv.From(ctx), qenv.From(context.Background())
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if fromCtx != s || fromGoroutine != s {
		t.Errorf("From in a task: %p by its context, %p by its goroutine; want %p", fromCtx, fromGoroutine, s)
	}
	if got := qenv.From(context.Background()); got != nil {
		t.Errorf("From outside a simulation = %p, want nil", got)
	}
}

// RestartAs leaves a process that is up as it is, and boots a crashed one
// as a new incarnation running the new code, as the new version.
func TestRestartAsBootsOnlyACrashedProcess(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	var boots []string
	boot := func(version string) func(ctx context.Context) {
		return func(ctx context.Context) {
			boots = append(boots, fmt.Sprintf("%s#%d", version, qenv.ProcessOf(ctx).Incarnation))
		}
	}
	p := s.Process("p", false, boot("v1"))
	run := func() {
		if err := s.Run(time.Second); err != nil {
			t.Fatal(err)
		}
	}
	run()
	p.RestartAs("v2", boot("v2"))
	run()
	if p.Version != "" || p.Incarnation != 1 {
		t.Errorf("RestartAs of a process that is up made it %q#%d, want it left as created", p.Version, p.Incarnation)
	}
	p.Crash()
	p.RestartAs("v2", boot("v2"))
	run()
	if want := []string{"v1#1", "v2#2"}; p.Version != "v2" || p.Incarnation != 2 || !slices.Equal(boots, want) {
		t.Errorf("after a crash, RestartAs: %q#%d, boots %v; want v2#2, boots %v", p.Version, p.Incarnation, boots, want)
	}
}

// svPlatform counts its machines' crashes and restarts; it runs nothing.
type svPlatform struct{ crashes, restarts map[string]int }

func (p *svPlatform) Machine(name string, main func()) qenv.Machine { return &svMachine{p, name} }

type svMachine struct {
	p    *svPlatform
	name string
}

func (m *svMachine) Crash()   { m.p.crashes[m.name]++ }
func (m *svMachine) Restart() { m.p.restarts[m.name]++ }

// On a Platform, a process's crash stops its machine, and its restart
// restarts it: without them a crashed process's goroutines run on (gosim
// stops them only by its machine), and a restarted one's tasks are handed
// to a machine that never starts them.
func TestNativeMachineFollowsItsProcess(t *testing.T) {
	plat := &svPlatform{crashes: map[string]int{}, restarts: map[string]int{}}
	s := qenv.NewNative(qenv.Nominal{}, plat)
	defer s.Close()
	p := s.Process("p", false, func(context.Context) {})
	p.Crash()
	p.Crash() // down already: nothing
	p.Restart()
	if plat.crashes["p"] != 1 || plat.restarts["p"] != 1 || p.Incarnation != 2 {
		t.Errorf("machine crashed %d times, restarted %d, the process at #%d; want once each, #2",
			plat.crashes["p"], plat.restarts["p"], p.Incarnation)
	}
}

// On the native runtime a Yield gives up the simulation's lock and takes
// it back: a task that spins, yielding until another task acts, lets that
// task run, and Run returns after its time.
func TestNativeYieldLetsOthersRun(t *testing.T) {
	s := qenv.NewNative(qenv.Nominal{}, nil)
	set, spun := false, false
	s.Spawn("spinner", func(ctx context.Context) {
		qenv.Go(ctx, "setter", func(context.Context) { set = true }) // can run only when the spinner yields the lock
		for !set {
			qenv.Yield(ctx)
		}
		spun = true
	})
	done := make(chan error, 1)
	go func() { done <- s.Run(20 * time.Millisecond) }() //qenv:ok Run in a goroutine, to fail rather than hang if a yield keeps the lock
	select {                                             //qenv:ok a real wait for Run, outside the simulation, or a timeout
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second): //qenv:ok the native runtime runs in real time
		t.Fatal("Run never returned: a Yield kept the simulation's lock")
	}
	s.Close()
	if !spun {
		t.Error("the spinner never saw the setter run")
	}
}

// A TimeProfile counts the virtual time a task waits at a seam.
func TestTimeProfileCountsASleep(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	s.Profile = qenv.NewTimeProfile()
	s.Spawn("sleeper", func(ctx context.Context) { qenv.Sleep(ctx, 3*time.Second) })
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := s.Profile.Total(); got != 3*time.Second {
		t.Errorf("profiled %v, want the 3s sleep", got)
	}
}

// A task made runnable while ids before it are held (their process runs
// slow, so they are off the run queue) still runs: the run queue grows to
// hold its id however many ids it skipped.
func TestRunQueueGrowsPastHeldTasks(t *testing.T) {
	sc := &qenv.Script{Faults: []qenv.Scripted{{Kind: "nemesis.slow.p"}}}
	s := qenv.New(sc)
	defer s.Close()
	s.Stalls = qenv.Stalls{Slow: time.Millisecond, SlowFor: time.Second}
	ran := map[string]bool{}
	s.Process("p", false, func(ctx context.Context) {
		for _, name := range []string{"w1", "w2"} { // held: p is slow
			qenv.Go(ctx, name, func(context.Context) { ran[name] = true })
		}
		s.Spawn("free", func(context.Context) { ran["free"] = true }) // of no process: runnable at once
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if !ran["w1"] || !ran["w2"] || !ran["free"] || len(sc.Remaining()) != 0 {
		t.Errorf("ran %v (scripted left: %v), want w1, w2 and free", ran, sc.Remaining())
	}
}

// Outside a simulation WaitFor polls until ready holds, before its time
// is up, and says so.
func TestWaitForOutsideASimulationEndsWhenReady(t *testing.T) {
	calls := 0
	ready := func() bool { calls++; return calls > 2 }
	if !qenv.WaitFor(context.Background(), ready, 10*time.Second) {
		t.Errorf("WaitFor = false after %d polls, want true once ready held", calls)
	}
}

// An Unsettled error says why: the stall it was, or the tasks still at
// work.
func TestUnsettledSaysWhy(t *testing.T) {
	stuck := &qenv.Unsettled{After: time.Second, Tasks: []string{"a: waiting"}, Stuck: errors.New("the stall")}
	if got := stuck.Error(); got != "qenv: not settled: the stall" {
		t.Errorf("stuck: %q", got)
	}
	busy := &qenv.Unsettled{After: 2 * time.Second, Tasks: []string{"a: runnable", "b: waiting"}}
	if got := busy.Error(); !strings.Contains(got, "not settled after 2s") || !strings.Contains(got, "a: runnable\n  b: waiting") {
		t.Errorf("busy: %q", got)
	}
}

// What did not finish is listed by task name, with its state, wherever it
// is reported (ErrStuck, Unsettled, Tasks): reports of the same run read
// the same, and a task waiting with a deadline says until when.
func TestUnfinishedTasksAreListedByName(t *testing.T) {
	t.Run("stuck", func(t *testing.T) {
		s := qenv.New(qenv.Nominal{})
		defer s.Close()
		var never qenv.Signal
		s.Spawn("zeta", func(ctx context.Context) { never.Wait(ctx, 0) })
		s.Spawn("alpha", func(ctx context.Context) { never.Wait(ctx, 0) })
		if err := s.Run(time.Second); !errors.Is(err, qenv.ErrStuck) || !strings.HasSuffix(err.Error(), "\n  alpha: waiting\n  zeta: waiting") {
			t.Errorf("Run = %v", err)
		}
	})
	t.Run("unsettled", func(t *testing.T) {
		s := qenv.New(qenv.Nominal{})
		defer s.Close()
		var never qenv.Signal
		s.Spawn("zeta", func(ctx context.Context) { never.Wait(ctx, time.Hour) })
		s.Spawn("alpha", func(ctx context.Context) { never.Wait(ctx, 0) })
		var u *qenv.Unsettled
		err := s.Settle(time.Second)
		if want := []string{"alpha: waiting", "zeta: waiting until 1h0m0s"}; !errors.As(err, &u) || !slices.Equal(u.Tasks, want) {
			t.Errorf("Settle = %v, want Unsettled with %q", err, want)
		}
	})
	t.Run("tasks", func(t *testing.T) {
		s := qenv.New(qenv.Nominal{})
		defer s.Close()
		var never qenv.Signal
		var got string
		s.Spawn("zeta.sleeper", func(ctx context.Context) { qenv.Sleep(ctx, time.Hour) })
		s.Spawn("beta.waiter", func(ctx context.Context) { never.Wait(ctx, 0) })
		s.Spawn("gamma.done", func(context.Context) {})
		s.Spawn("alpha.observer", func(ctx context.Context) {
			qenv.Sleep(ctx, time.Millisecond)
			s.Spawn("delta.new", func(context.Context) {})
			got = s.Tasks()
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		want := "alpha.observer: runnable\nbeta.waiter: waiting\ndelta.new: runnable\nzeta.sleeper: waiting until 1h0m0s"
		if got != want {
			t.Errorf("Tasks:\n%s\nwant:\n%s", got, want)
		}
	})
}

// MaxSteps bounds a run's scheduling steps exactly: a task that yields
// forever runs MaxSteps times, then Run reports the livelock.
func TestMaxStepsBoundsTheRunExactly(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	s.MaxSteps = 5
	runs := 0
	s.Spawn("spinner", func(ctx context.Context) {
		for {
			runs++
			qenv.Yield(ctx)
		}
	})
	if err := s.Run(time.Second); !errors.Is(err, qenv.ErrMaxSteps) || runs != 5 {
		t.Errorf("Run = %v after %d runs of the task, want ErrMaxSteps after 5", err, runs)
	}
}

// A task chooser (PCT) is asked, and its pick recorded as a schedule
// choice, only where more than one task could run: one runnable task is
// no choice, so a log of one task's run has none, as NewPCT's doc counts
// them.
func TestPCTIsAskedOnlyBetweenTasks(t *testing.T) {
	for _, tasks := range []int{1, 2} {
		log := &qenv.ChoiceLog{C: qenv.NewPCT(qenv.Nominal{}, 1, 2, 100)}
		s := qenv.New(log)
		for i := range tasks {
			s.Spawn(fmt.Sprint("t", i), svYields(5))
		}
		if err := s.Run(time.Second); err != nil {
			t.Fatal(err)
		}
		s.Close()
		sched := 0
		for _, c := range log.Choices {
			if c.Kind == "sched.task" {
				sched++
				if c.N < 2 {
					t.Errorf("%d tasks: a schedule choice of %d options: %v", tasks, c.N, c)
				}
			}
		}
		if (sched > 0) != (tasks > 1) {
			t.Errorf("%d tasks: %d schedule choices", tasks, sched)
		}
	}
}

// Predicate waiters that become runnable together are offered in task
// (creation) order, whatever order they began to wait in: option 0 is
// the oldest, as for every schedule choice.
func TestPredicateWaitersAreOfferedInIDOrder(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	flag := false
	var order []string
	s.Spawn("first", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Millisecond) // waits after second does
		qenv.Wait(ctx, func() bool { return flag })
		order = append(order, "first")
	})
	s.Spawn("second", func(ctx context.Context) {
		qenv.Wait(ctx, func() bool { return flag })
		order = append(order, "second")
	})
	s.Spawn("setter", func(ctx context.Context) {
		qenv.Sleep(ctx, 2*time.Millisecond)
		flag = true
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if want := []string{"first", "second"}; !slices.Equal(order, want) {
		t.Errorf("ran %v, want %v", order, want)
	}
}

// A settle's turn goes to the first runnable task after the one that ran
// last, a predicate waiter counted where its id is: when a sleeper's
// timer and the last task's own predicate come due together, the sleeper
// (the next after it, wrapping around) runs first, not the last task
// again.
func TestSettleTurnsPastTheTaskThatRan(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	var order []string
	s.Spawn("sleeper", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Second)
		order = append(order, "sleeper")
	})
	s.Spawn("watcher", func(ctx context.Context) {
		qenv.Wait(ctx, func() bool { return s.Elapsed() >= time.Second })
		order = append(order, "watcher")
	})
	if err := s.Settle(time.Minute); err != nil {
		t.Fatal(err)
	}
	if want := []string{"sleeper", "watcher"}; !slices.Equal(order, want) {
		t.Errorf("ran %v, want %v", order, want)
	}
}

// A settle that finds the system quiet records it through the run's
// recorder: the binding's eventual properties then apply to the trace.
func TestSettleRecordsTheSettle(t *testing.T) {
	sink := &qobs.MemorySink{}
	rec := qobs.NewRecorder(sink)
	s := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(context.Background(), rec)))
	defer s.Close()
	s.Spawn("client", func(ctx context.Context) { qenv.Sleep(ctx, time.Second) })
	if err := s.Settle(time.Minute); err != nil {
		t.Fatal(err)
	}
	steps := sink.Steps()
	if len(steps) != 1 || !steps[0].Settled {
		t.Errorf("recorded %+v, want one settled record", steps)
	}
}

// A nemesis choice the log records names the nemesis chosen, not only its
// index among those enabled then: what a report and a Script go by.
func TestNemesisChoiceIsRecordedByName(t *testing.T) {
	log := &qenv.ChoiceLog{C: &svOnce{kind: "fault.nemesis.crash", v: 1}}
	s := qenv.New(log)
	defer s.Close()
	s.Process("p", true, svYields(3))
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(log.Choices, func(c qenv.Choice) bool { return c.Kind == "fault.nemesis.crash" && c.Value == 1 })
	if i < 0 || log.Choices[i].Name != "crash.p" {
		t.Errorf("choices %v: want the crash named crash.p", log.Choices)
	}
}

// Stalls are fixed once the first process is made: a change after it, by
// the next process or by Run, panics, rather than give processes made
// before it other stalls (or none) silently.
func TestStallsAreFixedByTheFirstProcess(t *testing.T) {
	for _, c := range []struct {
		name string
		do   func(s *qenv.Sim)
	}{
		{"before another process", func(s *qenv.Sim) {
			s.Process("a", false, svYields(1))
			s.Stalls = qenv.Stalls{Pause: time.Second}
			s.Process("b", false, svYields(1))
			_ = s.Run(time.Second)
		}},
		{"before Run", func(s *qenv.Sim) {
			s.Process("a", false, svYields(1))
			s.Stalls = qenv.Stalls{Pause: time.Second}
			_ = s.Run(time.Second)
		}},
	} {
		s := qenv.New(qenv.Nominal{})
		if got := svRecover(func() { c.do(s) }); !strings.Contains(got, "Sim.Stalls changed after the first process") {
			t.Errorf("%s: panicked with %q, want the change refused", c.name, got)
		}
		s.Close()
	}
}

// svNarrow records the option counts it is asked to choose among.
type svNarrow struct{ asked map[string][]int }

func (c *svNarrow) Choose(kind string, n int) int {
	c.asked[kind] = append(c.asked[kind], n)
	return 0
}

// In a core settle, a fault choice outside the core is drawn as in a run,
// but one with a single option is no choice, and the chooser is not asked
// it (a Chooser chooses among two or more).
func TestCoreSettleAsksNoChoiceOfOneOption(t *testing.T) {
	k := qenv.DeclareFaults("svcore.one", "x")
	c := &svNarrow{asked: map[string][]int{}}
	s := qenv.New(c)
	defer s.Close()
	a := s.Process("a", false, func(context.Context) {})
	s.Process("b", false, func(ctx context.Context) {
		qenv.Sleep(ctx, time.Millisecond) // in the settle
		s.FaultOf(k.Only())
		s.FaultOf(k)
	})
	if err := s.SettleCore(time.Second, a); err != nil {
		t.Fatal(err)
	}
	if got := c.asked["fault.svcore.one"]; !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("asked fault.svcore.one with %v options, want only the choice of 2", got)
	}
}
