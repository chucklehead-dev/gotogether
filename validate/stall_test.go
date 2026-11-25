package validate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A late response is no verdict when a host stall within its window
// covers its lateness: a leader 4s after a heal (bound 3s, 1s late) with
// the host stalled 1.5s between them. The same run without the stall, a
// stall outside the window, one shorter than the lateness (and two that
// overlap, counted once) FAIL; a response that never came FAILs whatever
// stalled; and the report excused is a copy, the original's FAIL kept
// for runs of the same steps that did not stall.
func TestExcuseStalls(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker(t.Context(), "testdata/within.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	name := c.Binding.Within[0].Name()
	sec := func(f float64) time.Time { return time.Unix(0, 0).Add(time.Duration(f * float64(time.Second))) }
	history := func(steps ...any) []qtrace.Step {
		var out []qtrace.Step
		for i := 0; i < len(steps); i += 2 {
			out = append(out, qtrace.Step{Action: steps[i].(string), Recorder: "n", Seq: uint64(i/2 + 1), Time: sec(steps[i+1].(float64))})
		}
		last := out[len(out)-1]
		return append(out, qtrace.Step{Action: "settled", Record: qtrace.Record{Settled: true}, Recorder: "n", Seq: last.Seq + 1, Time: last.Time})
	}
	stall := func(from, to float64) qtrace.Stall {
		return qtrace.Stall{Start: sec(from), End: sec(to), Kind: "sched"}
	}
	late := history("crash", 0.0, "heal", 1.0, "elect", 5.0)
	verdict := func(r *Report) Verdict {
		for _, inv := range r.Actors[0].Invariants {
			if inv.Name == name {
				return inv.Verdict
			}
		}
		return ""
	}
	for _, x := range []struct {
		name   string
		steps  []qtrace.Step
		stalls []qtrace.Stall
		want   Verdict
	}{
		{"late, no stall", late, nil, Fail},
		{"late, a stall covering its lateness", late, []qtrace.Stall{stall(2, 3.5)}, NotEvaluated},
		{"late, a stall after the response", late, []qtrace.Stall{stall(6, 8)}, Fail},
		{"late, a stall before the trigger", late, []qtrace.Stall{stall(0, 0.9)}, Fail},
		{"late, a stall shorter than its lateness", late, []qtrace.Stall{stall(2, 2.5)}, Fail},
		{"late, two stalls overlapping, short together", late, []qtrace.Stall{stall(2, 2.6), stall(2.1, 2.7)}, Fail},
		{"late, two stalls overlapping, long enough", late, []qtrace.Stall{stall(2, 2.6), stall(2.4, 3.1)}, NotEvaluated},
		{"late, a stall half in the window", late, []qtrace.Stall{stall(4.5, 6)}, Fail},
		{"never", history("crash", 0.0, "heal", 1.0, "tick", 2.0), []qtrace.Stall{stall(1.5, 4.5)}, Fail},
		{"in time", history("crash", 0.0, "heal", 1.0, "elect", 2.0), []qtrace.Stall{stall(1.2, 1.8)}, Pass},
	} {
		rep, err := c.Check(context.Background(), x.steps)
		if err != nil {
			t.Fatalf("%s: %v", x.name, err)
		}
		before := verdict(rep)
		got := rep.ExcuseStalls(x.stalls)
		if v := verdict(got); v != x.want {
			t.Errorf("%s: %s %q, want %q\n%s", x.name, name, v, x.want, got.Summary())
		}
		if verdict(rep) != before {
			t.Errorf("%s: ExcuseStalls changed the report it was given: %q, was %q", x.name, verdict(rep), before)
		}
		excused := x.want == NotEvaluated
		if got.Stalled() != excused || got.OK() != (x.want == Pass) {
			t.Errorf("%s: Stalled %v, OK %v; want Stalled %v, OK %v", x.name, got.Stalled(), got.OK(), excused, x.want == Pass)
		}
		if !excused {
			continue
		}
		v := got.Verdict()
		if v.Verdict != NotEvaluated || v.Reason != "no verdict (host stall)" {
			t.Errorf("%s: the run's verdict %s %q, want n/a, no verdict (host stall)", x.name, v.Verdict, v.Reason)
		}
		sum := got.Summary()
		for _, want := range []string{"no verdict (host stall)", "a sched stall of", "1s late"} {
			if !strings.Contains(sum, want) {
				t.Errorf("%s: the report does not say %q:\n%s", x.name, want, sum)
			}
		}
		if ab := got.Absent(); len(ab) != 1 || ab[0].Cause != Stalled {
			t.Errorf("%s: absent %v, want one, stalled", x.name, ab)
		}
	}
}

// StalledWithin counts each stalled instant once, within the window.
func TestStalledWithin(t *testing.T) {
	t.Parallel()
	at := func(ms int) time.Time { return time.Unix(0, 0).Add(time.Duration(ms) * time.Millisecond) }
	s := func(a, b int) qtrace.Stall { return qtrace.Stall{Start: at(a), End: at(b)} }
	for _, x := range []struct {
		stalls   []qtrace.Stall
		from, to int
		want     time.Duration
		n        int
	}{
		{nil, 0, 100, 0, 0},
		{[]qtrace.Stall{s(10, 20)}, 0, 100, 10 * time.Millisecond, 1},
		{[]qtrace.Stall{s(10, 30), s(20, 40)}, 0, 100, 30 * time.Millisecond, 2},
		{[]qtrace.Stall{s(10, 30), s(50, 60)}, 0, 100, 30 * time.Millisecond, 2},
		{[]qtrace.Stall{s(10, 300)}, 0, 100, 90 * time.Millisecond, 1},
		{[]qtrace.Stall{s(100, 300)}, 0, 100, 0, 0},
	} {
		got, in := qtrace.StalledWithin(x.stalls, at(x.from), at(x.to))
		if got != x.want || len(in) != x.n {
			t.Errorf("%v in [%d, %d]: %v of %d stalls, want %v of %d", x.stalls, x.from, x.to, got, len(in), x.want, x.n)
		}
	}
}
