package qenv_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// drainer is a process's main task that drains at its termination: it
// waits for the signal (Terminating's context), works for work, and
// exits; up counts its starts.
func drainer(work time.Duration, up *int) func(ctx context.Context) {
	return func(ctx context.Context) {
		*up++
		term := qenv.Terminating(ctx)
		qenv.WaitIdle(ctx, func() bool { return term.Err() != nil })
		qenv.Sleep(ctx, work)
		qenv.ProcessOf(ctx).Exit()
	}
}

// A graceful termination: the signal cancels the contexts Terminating
// gave the process's tasks, the process drains within its grace and
// exits, and is restarted as its next incarnation, never killed. One
// that ignores the signal is killed at the grace deadline, and restarted
// too.
func TestTerminateDrainsOrKills(t *testing.T) {
	for _, tc := range []struct {
		name string
		work time.Duration // the drain's; 0: the process ignores the signal
		want string        // its end: "exit" or "kill"
		at   time.Duration // when the second incarnation starts
	}{
		{"drains", 2 * time.Second, "exit", 2 * time.Second},
		{"ignores", 0, "kill", 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := qenv.New(qenv.Nominal{})
			s.Check = true
			defer s.Close()
			var log []string
			s.Log = func(l string) { log = append(log, l) }
			up := 0
			start := drainer(tc.work, &up)
			if tc.work == 0 {
				start = func(ctx context.Context) { up++; qenv.Daemon(ctx); qenv.Wait(ctx, func() bool { return false }) }
			}
			p := s.Process("p", false, start)
			s.Terminations(qenv.Termination{Grace: 10 * time.Second}, p)
			var restarted time.Duration
			s.Spawn("operator", func(ctx context.Context) {
				qenv.Yield(ctx)
				p.Terminate()
				if !p.Terminating() {
					t.Error("Terminating() false after the signal")
				}
				p.Terminate() // a second signal while it drains changes nothing
				qenv.Wait(ctx, func() bool { return p.Incarnation == 2 && !p.Down() })
				restarted = s.Elapsed()
			})
			if err := s.Run(time.Minute); err != nil {
				t.Fatal(err)
			}
			if restarted != tc.at || up != 2 || p.Terminating() {
				t.Errorf("restarted at %v (want %v), started %d times (want 2), terminating %v after the restart", restarted, tc.at, up, p.Terminating())
			}
			joined := strings.Join(log, "\n")
			ended := "exit p#1"
			if tc.want == "kill" {
				ended = "kill p#1 at its grace"
			}
			if !strings.Contains(joined, "terminate p#1 (grace 10s)") || !strings.Contains(joined, ended) {
				t.Errorf("log %q: want the signal, then %q", log, ended)
			}
			if tc.want == "exit" && strings.Contains(joined, "kill") {
				t.Errorf("a drain within its grace was killed: %q", log)
			}
		})
	}
}

// Option short of terminate.<name>.grace kills a drain that the full
// grace would have let finish; option 0 is the full grace.
func TestTerminateShortGrace(t *testing.T) {
	for _, short := range []bool{false, true} {
		var faults []qenv.Scripted
		if short {
			faults = []qenv.Scripted{{Kind: "terminate.p.grace", Name: "short"}}
		}
		s := qenv.New(&qenv.Script{Faults: faults})
		var log []string
		s.Log = func(l string) { log = append(log, l) }
		up := 0
		p := s.Process("p", false, drainer(3*time.Second, &up))
		s.Terminations(qenv.Termination{Grace: 10 * time.Second, Short: time.Second}, p)
		s.Spawn("operator", func(ctx context.Context) { p.Terminate() })
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		killed := strings.Contains(strings.Join(log, "\n"), "kill p#1")
		if killed != short || up != 2 {
			t.Errorf("short %v: killed %v, started %d times: %q", short, killed, up, log)
		}
		s.Close()
	}
}

// The terminate nemeses: a Script names one, and a budget (a
// PodDisruptionBudget) keeps more than Budget of a group from
// terminating at once, however often the scheduler sends the signal.
// Without the budget, two terminate together in some run.
func TestTerminationsBudget(t *testing.T) {
	// a Script's nemesis by name
	s := qenv.New(&qenv.Script{Faults: []qenv.Scripted{{Kind: "nemesis.terminate.b"}}})
	up := 0
	a := s.Process("a", false, drainer(time.Second, &up))
	b := s.Process("b", false, drainer(time.Second, &up))
	s.Terminations(qenv.Termination{}, a, b)
	s.Spawn("work", func(ctx context.Context) {
		for i := 0; i < 20; i++ {
			qenv.Yield(ctx)
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if a.Incarnation != 1 || b.Incarnation != 2 {
		t.Errorf("incarnations a %d, b %d: want b alone terminated (1, 2)", a.Incarnation, b.Incarnation)
	}
	s.Close()
	most := func(budget int) int {
		most := 0
		for seed := uint64(1); seed <= 20; seed++ {
			c := qenv.NewSeeded(seed)
			c.Rates = qenv.NoFaults.Set("nemesis.terminate", 0.3)
			s := qenv.New(c)
			s.Check = true
			up := 0
			var procs []*qenv.Process
			for _, n := range []string{"a", "b", "c"} {
				procs = append(procs, s.Process(n, false, drainer(time.Second, &up)))
			}
			s.Terminations(qenv.Termination{Budget: budget}, procs...)
			s.Spawn("watch", func(ctx context.Context) {
				for i := 0; i < 200; i++ {
					n := 0
					for _, p := range procs {
						if p.Terminating() {
							n++
						}
					}
					most = max(most, n)
					qenv.Sleep(ctx, 100*time.Millisecond)
				}
			})
			if err := s.Run(time.Minute); err != nil {
				t.Fatal(err)
			}
			s.Close()
		}
		return most
	}
	if m := most(1); m != 1 {
		t.Errorf("budget 1: at most %d terminating at once, want 1", m)
	}
	if m := most(0); m < 2 {
		t.Errorf("no budget: at most %d terminating at once, want 2 or more in some run", m)
	}
}

// A call's context cancelled while it waits (Terminating's, at the
// signal) ends the call with the context's error, though the handler
// runs on: the outcome unknown, as a real client's. A call made with a
// cancelled context sends nothing.
func TestCallCancelled(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	s.Check = true
	defer s.Close()
	svc := qenv.NewService(s, "bank", nil, func(ctx context.Context, n int) (int, error) {
		qenv.Sleep(ctx, time.Second)
		return n, nil
	})
	var errs []error
	p := s.Process("node", false, func(ctx context.Context) {
		term := qenv.Terminating(ctx)
		_, err := svc.Call(term, 1, 5*time.Second)
		errs = append(errs, err)
		_, err = svc.Call(term, 2, 5*time.Second)
		errs = append(errs, err)
		if qenv.ProcessOf(ctx).Incarnation == 1 {
			qenv.Daemon(ctx)
			qenv.Wait(ctx, func() bool { return false }) // the first incarnation's ends at its kill
		}
	})
	s.Spawn("operator", func(ctx context.Context) {
		qenv.Sleep(ctx, 500*time.Millisecond)
		p.Terminate()
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	// the incarnation after the kill calls again, its context never cancelled
	if len(errs) != 4 || !errors.Is(errs[0], context.Canceled) || !errors.Is(errs[1], context.Canceled) || errs[2] != nil || errs[3] != nil || svc.Calls != 3 {
		t.Errorf("errors %v, %d handler calls: want the first two cancelled (the first handled), then two answered", errs, svc.Calls)
	}
}

// A router's view: without lag a terminating backend leaves it at the
// signal and its next incarnation joins at its restart; under
// route.<name>.lag's stale option each change reaches the view a lag
// later, in order: the terminating endpoint still chosen, then gone, and
// the restarted one not yet there.
func TestRouterLag(t *testing.T) {
	type sample struct {
		at   time.Duration
		view string
	}
	run := func(stale bool) []sample {
		var faults []qenv.Scripted
		if stale {
			faults = []qenv.Scripted{{Kind: "route.api.lag", Name: "stale"}} // the removal's: the restart's is nominal
		}
		s := qenv.New(&qenv.Script{Faults: faults})
		s.Check = true
		defer s.Close()
		up := 0
		a := s.Process("a", false, drainer(time.Second, &up))
		b := s.Process("b", false, drainer(time.Second, &up))
		s.Terminations(qenv.Termination{}, a, b)
		r := qenv.NewRouter(s, "api", 3*time.Second, a, b)
		var out []sample
		s.Spawn("probe", func(ctx context.Context) {
			a.Terminate()
			for i := 0; i <= 10; i++ {
				var names []string
				for _, e := range r.View() {
					n := e.String()
					if e.Gone() {
						n += "(gone)"
					}
					names = append(names, n)
				}
				out = append(out, sample{s.Elapsed(), strings.Join(names, " ")})
				qenv.Sleep(ctx, 500*time.Millisecond)
			}
		})
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		return out
	}
	at := func(ss []sample, d time.Duration) string {
		i := slices.IndexFunc(ss, func(s sample) bool { return s.at == d })
		if i < 0 {
			return "?"
		}
		return ss[i].view
	}
	nominal := run(false)
	if v0, v1 := at(nominal, 0), at(nominal, 1500*time.Millisecond); v0 != "b#1" || v1 != "a#2 b#1" {
		t.Errorf("no lag: view %q at the signal, %q after the restart: want b#1, then a#2 b#1", v0, v1)
	}
	stale := run(true)
	// the exit at 1 s, the restart with it: a#1 stays (gone after the
	// exit) until 3 s, and a#2, whose change was nominal, joins no
	// earlier than the removal before it
	for d, want := range map[time.Duration]string{0: "a#1 b#1", 1500 * time.Millisecond: "a#1(gone) b#1", 3 * time.Second: "a#2 b#1"} {
		if got := at(stale, d); got != want {
			t.Errorf("stale: view at %v %q, want %q", d, got, want)
		}
	}
}

// Pick chooses among the view's endpoints by the schedule choice
// route.<name>, and fails with ErrNoEndpoint on an empty view.
func TestRouterPick(t *testing.T) {
	c := qenv.NewSeeded(3)
	s := qenv.New(c)
	defer s.Close()
	a := s.Process("a", false, func(context.Context) {})
	b := s.Process("b", false, func(context.Context) {})
	r := qenv.NewRouter(s, "api", 0, a, b)
	picked := map[string]int{}
	s.Spawn("client", func(ctx context.Context) {
		for i := 0; i < 40; i++ {
			e, err := r.Pick(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			picked[e.Process.Name]++
		}
		a.Terminate()
		b.Terminate()
		if _, err := r.Pick(ctx); !errors.Is(err, qenv.ErrNoEndpoint) {
			t.Errorf("Pick with both terminating: %v, want ErrNoEndpoint", err)
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if picked["a"] == 0 || picked["b"] == 0 {
		t.Errorf("picks %v: want both backends chosen", picked)
	}
}

// A process down counts against a Budget as one terminating does: with
// a budget of one and a crashed process that nothing restarts (an
// unhealthy pod, which a PodDisruptionBudget counts), no other process
// of the group is terminated, however often the scheduler would; once
// it is back, one is.
func TestTerminationsBudgetCountsDown(t *testing.T) {
	c := qenv.NewSeeded(1)
	c.Rates = qenv.NoFaults.Set("nemesis.terminate", 1)
	s := qenv.New(c)
	s.Check = true
	defer s.Close()
	up := 0
	a := s.Process("a", false, drainer(time.Second, &up))
	b := s.Process("b", false, drainer(time.Second, &up))
	var during, after int // b's incarnation while a is down, and at the end
	s.Spawn("operator", func(ctx context.Context) {
		a.Crash() // stays down: a is not crashable, nothing restarts it
		s.Terminations(qenv.Termination{Budget: 1}, a, b)
		for i := 0; i < 50; i++ {
			qenv.Sleep(ctx, 100*time.Millisecond)
		}
		during = b.Incarnation
		a.Restart()
		qenv.Sleep(ctx, 10*time.Second)
		after = b.Incarnation + a.Incarnation
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if during != 1 {
		t.Errorf("b at incarnation %d while a was down: a budget of one let a second process go", during)
	}
	if after <= 3 {
		t.Errorf("incarnations a+b %d once a was back: want a termination then", after)
	}
}

// Terminating's contexts that ended before the signal are dropped, so a
// task asking for one per request keeps no more than are live.
func TestTerminatingDropsEnded(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	var p *qenv.Process
	p = s.Process("p", false, func(ctx context.Context) {
		for i := 0; i < 100; i++ {
			c, cancel := context.WithCancel(ctx)
			_ = qenv.Terminating(c)
			cancel()
		}
		keep := qenv.Terminating(ctx)
		if n := qenv.TermContexts(p); n > 2 {
			t.Errorf("%d contexts kept after 100 requests ended, want at most 2", n)
		}
		p.Terminate()
		if keep.Err() == nil {
			t.Error("a live context was not cancelled at the signal")
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
}

// lagAt is a chooser making route.<name>.lag's choices whose ordinal
// (from 1) is in stale its stale option, every other choice nominal.
type lagAt struct {
	kind  string
	stale map[int]bool
	n     int
}

func (c *lagAt) Choose(kind string, n int) int {
	if kind != "fault."+c.kind {
		return 0
	}
	c.n++
	if c.stale[c.n] {
		return 1
	}
	return 0
}

// A router's changes reach its view in the order they happened, every
// backend's in one sequence: a rolling restart that terminates b once a
// is back never empties the view, though a's return reaches it late and
// b's removal is on time. A lag per change, each backend's apart, let b's
// removal overtake a's return and emptied the view for the lag.
func TestRouterKeepsOrderAcrossBackends(t *testing.T) {
	// the lag's choices: a's removal (1, nominal), a's return (2, stale),
	// b's removal (3, nominal)
	s := qenv.New(&lagAt{kind: "route.api.lag", stale: map[int]bool{2: true}})
	s.Check = true
	defer s.Close()
	up := 0
	a := s.Process("a", false, drainer(time.Second, &up))
	b := s.Process("b", false, drainer(10*time.Second, &up))
	s.Terminations(qenv.Termination{}, a, b)
	r := qenv.NewRouter(s, "api", 3*time.Second, a, b)
	var views []string
	s.Spawn("rollout", func(ctx context.Context) {
		a.Terminate()
		qenv.Wait(ctx, func() bool { return a.Incarnation == 2 && !a.Down() })
		b.Terminate()
		for i := 0; i < 20; i++ {
			var names []string
			for _, e := range r.View() {
				names = append(names, e.String())
			}
			views = append(views, fmt.Sprintf("%v %s", s.Elapsed(), strings.Join(names, " ")))
			qenv.Sleep(ctx, 250*time.Millisecond)
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if strings.HasSuffix(v, " ") {
			t.Errorf("the view emptied at %s: %q", strings.TrimSpace(v), views)
			break
		}
	}
	if want := "4s a#2"; !slices.Contains(views, want) {
		t.Errorf("views %q: want %q once a's return and b's removal reached it, in order", views, want)
	}
}
