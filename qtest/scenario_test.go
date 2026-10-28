package qtest

import (
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// the calls of a step list: each "action.outcome", or "action.outcome(x)"
// with argument x of parameter p
func scenarioCalls(steps ...string) []binding.Call {
	var out []binding.Call
	for i, s := range steps {
		a, o, _ := strings.Cut(s, ".")
		c := binding.Call{Action: a, Quint: s, Args: map[string]string{}}
		if o, arg, ok := strings.Cut(o, "("); ok {
			c.Args["p"] = strings.TrimSuffix(arg, ")")
			c.Step = &qtrace.Step{Action: a, Outcome: o, Seq: uint64(i)}
		} else {
			c.Step = &qtrace.Step{Action: a, Outcome: o, Seq: uint64(i)}
		}
		if c.Step.Outcome == "ok" {
			c.Step.Outcome = ""
		}
		out = append(out, c)
	}
	return out
}

// Scenario's matching against brute force on every step list of up to
// five steps over two actions and two outcomes, and every scenario of
// up to three steps over five Takes: it holds exactly when some
// increasing choice of steps takes the scenario's in turn, its prefix
// is the longest the steps take in turn, and each matched step takes
// its Take. Ablating the order (a step matched anywhere) or the outcome
// filter fails it.
func TestScenarioAgainstBruteForce(t *testing.T) {
	t.Parallel()
	symbols := []string{"a.ok", "a.x", "b.ok", "b.x"}
	takes := []Take{Takes("a"), Takes("a").Outcome("ok"), Takes("a").Outcome("x"), Takes("b"), Takes("b").Outcome("x")}
	var lists [][]string
	var gen func(prefix []string)
	gen = func(prefix []string) {
		lists = append(lists, prefix)
		if len(prefix) == 5 {
			return
		}
		for _, s := range symbols {
			gen(append(append([]string(nil), prefix...), s))
		}
	}
	gen(nil)
	var scens [][]Take
	var genS func(prefix []Take)
	genS = func(prefix []Take) {
		if len(prefix) > 0 {
			scens = append(scens, prefix)
		}
		if len(prefix) == 3 {
			return
		}
		for _, k := range takes {
			genS(append(append([]Take(nil), prefix...), k))
		}
	}
	genS(nil)
	// brute force, on the symbols themselves: does some increasing choice
	// of steps take the scenario's in turn?
	takesSym := func(k Take, sym string) bool {
		a, o, _ := strings.Cut(sym, ".")
		return k.action == a && (k.outcome == "" || k.outcome == o)
	}
	var sub func(syms []string, steps []Take) bool
	sub = func(syms []string, steps []Take) bool {
		if len(steps) == 0 {
			return true
		}
		for i, c := range syms {
			if takesSym(steps[0], c) && sub(syms[i+1:], steps[1:]) {
				return true
			}
		}
		return false
	}
	checked := 0
	for _, l := range lists {
		calls := scenarioCalls(l...)
		for _, steps := range scens {
			sc := &scenario{steps: steps}
			n, at := sc.match(calls)
			longest := 0
			for k := 1; k <= len(steps); k++ {
				if sub(l, steps[:k]) {
					longest = k
				}
			}
			if n != longest {
				t.Fatalf("%v over %v: matched %d, the longest prefix taken %d", sc, l, n, longest)
			}
			if (n == len(steps)) != sub(l, steps) {
				t.Fatalf("%v over %v: holds %v, brute force %v", sc, l, n == len(steps), sub(l, steps))
			}
			for i := range n {
				if !takesSym(steps[i], l[at[i]]) || i > 0 && at[i] <= at[i-1] {
					t.Fatalf("%v over %v: step %d at call %d", sc, l, i, at[i])
				}
			}
			checked++
		}
	}
	if checked < 100000 {
		t.Fatalf("only %d cases", checked)
	}
}

// A run that misses a scenario is reported with the prefix it took,
// each at its step, where it stopped, and the next step's near misses;
// With compares arguments as Quint values.
func TestScenarioMiss(t *testing.T) {
	t.Parallel()
	sc := NamedScenario("happy path", Takes("submit"), Takes("verify").Outcome("ok"), Takes("open").Outcome("ok").Hint("send an open after the verification")).scenario
	calls := scenarioCalls("submit.ok", "verify.denied", "verify.ok", "close.ok", "open.denied")
	got := sc.miss(calls)
	for _, want := range []string{`2 of 3 steps of the scenario "happy path" (submit -> verify with outcome ok -> open with outcome ok) taken (submit at step 1, verify with outcome ok at step 3)`,
		"stuck at open with outcome ok: no step after step 3 of the run's 5 takes it", "open taken otherwise: step 5 open.denied with outcome denied", "send an open after the verification"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s\nwant %q in it", got, want)
		}
	}
	with := Scenario(Takes("put").With("p", `Set(1, 2)`)).scenario
	if n, _ := with.match(scenarioCalls("put.ok(Set(2,1))")); n != 1 {
		t.Errorf("With: a set in another order is the same value")
	}
	if n, _ := with.match(scenarioCalls("put.ok(Set(3))")); n != 0 {
		t.Errorf("With: another value matched")
	}
}

// Over runs, a scenario is an expectation like any: Some wants a run
// that follows it, None none, All every run; a claim no run meets lists
// each run's prefix. The actors' steps are merged by their times.
func TestScenarioOverRuns(t *testing.T) {
	t.Parallel()
	run := func(steps ...string) *Run {
		return &Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass, Calls: scenarioCalls(steps...)}}}}
	}
	sc := Scenario(Takes("submit"), Takes("open"))
	full, half := run("submit.ok", "noise.ok", "open.ok"), run("open.ok", "submit.ok")
	for _, c := range []struct {
		claim Claim
		runs  []*Run
		ok    bool
	}{
		{Some(sc), []*Run{half, full}, true},
		{Some(sc), []*Run{half}, false},
		{All(sc), []*Run{half, full}, false},
		{All(sc), []*Run{full, full}, true},
		{None(sc), []*Run{half}, true},
		{None(sc), []*Run{half, full}, false},
	} {
		x := Runs(c.runs...)
		reps := x.reports()
		if j := c.claim.judge(reps); j.ok != c.ok {
			t.Errorf("%s over %d runs: %v, want %v", c.claim, len(c.runs), j.ok, c.ok)
		}
	}
	x := Runs(half, run("submit.ok"))
	if msg := x.verdict(Some(sc), x.reports()).say(t); !strings.Contains(msg, "run 1: 1 of 2 steps") || !strings.Contains(msg, "run 2: 1 of 2 steps") {
		t.Errorf("a scenario no run follows: %s", msg)
	}
	// two actors: submit at t=2 by one, open at t=1 by the other is no scenario
	t0 := time.Unix(100, 0)
	two := func(submitAt, openAt int) *Run {
		a, b := scenarioCalls("submit.ok"), scenarioCalls("open.ok")
		a[0].Step.Time, b[0].Step.Time = t0.Add(time.Duration(submitAt)), t0.Add(time.Duration(openAt))
		return &Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass, Calls: a}, {Conforms: validate.Pass, Calls: b}}}}
	}
	if sc.check(two(1, 2).Report) != true || sc.check(two(2, 1).Report) != false {
		t.Errorf("two actors: the merge by time")
	}
}

// A With naming no parameter of its action never matches: None of a
// scenario through it would hold for nothing, so the claim fails first,
// naming the parameter and the action's signature, as a Take's does.
func TestScenarioUnknownParameter(t *testing.T) {
	t.Parallel()
	c := &validate.Checker{Binding: &binding.Binding{}, Model: &binding.Model{Module: "m",
		Actions: map[string][]binding.Param{"put": {{Name: "item", Type: "str"}}, "submit": nil}}}
	run := &Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass, Calls: scenarioCalls("submit.ok", "put.ok(1)")}}}, checker: c}
	for _, claim := range []Claim{
		None(Scenario(Takes("submit"), Takes("put").With("itm", `"a"`))),
		Takes("put").With("itm", `"a"`),
		TakesEach(Takes("put").With("itm", `"a"`)),
	} {
		f := &failTB{TB: t}
		done := make(chan struct{})
		go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
			defer close(done)
			Runs(run).Expect(f, claim)
		}()
		<-done
		if msg := strings.Join(f.errs, "\n"); !strings.Contains(msg, "put has no parameter itm (did you mean item?; signature") {
			t.Errorf("%s: %q, want the parameter named", claim, msg)
		}
	}
	// a parameter it has: judged as usual (None holds: no put with item "a")
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		Runs(run).Expect(f, None(Scenario(Takes("submit"), Takes("put").With("item", `"a"`))))
	}()
	<-done
	if f.failed {
		t.Errorf("a known parameter: %q", f.errs)
	}
}

// A Take's outcome reaches the model's search only through the parameter
// the binding passes it: with none (or an outcome the binding skips),
// the search would look for the action with any outcome and call
// possible what the outcome makes impossible, so it is no search, saying
// why and what to do.
func TestScenarioOutcomeWithoutParameter(t *testing.T) {
	t.Parallel()
	c := &validate.Checker{Binding: &binding.Binding{Actions: map[string]binding.Action{
		"buy":    {Args: map[string]string{"coin": "{{.coin}}"}},
		"refund": {Args: map[string]string{"result": "{{ .outcome }}"}, Outcomes: map[string]string{"lost": "skip"}},
	}}}
	for _, k := range []Take{Takes("buy").Outcome("short"), Takes("refund").Outcome("lost")} {
		_, err := scenarioAsPossible(c.Binding, Scenario(k).scenario)
		if err == nil || !strings.Contains(err.Error(), "passes "+k.action+"'s outcome to none of its parameters") || !strings.Contains(err.Error(), "drop the Outcome") {
			t.Errorf("%s: %v, want no search, saying why", k.what(), err)
		}
	}
	if p, v, ok := outcomeParam(c.Binding, "refund", "late"); !ok || p != "result" || v != `"late"` {
		t.Errorf("an outcome the binding passes: %s = %s (%v)", p, v, ok)
	}
}
