package validate

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/replay"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// The Go evaluator decides the runs that pass; quint judges the others.
//
// A run's check replays each actor's plan. When the binding's model is in
// the evaluator's fragment (its evaluation module compiles, and nothing
// the run's steps take is refused), the evaluator replays the plan first,
// as quint's fused replay would: each call taken from the state before it,
// its expect holding after it, every invariant in every state, and, in a
// settled trace, every eventual val in the last one. A plan it accepts is
// a pass, with no quint process. A plan it rejects goes to quint's detail
// replay (the runs that say which check failed, and where), and one it
// refuses, or cannot judge, to quint's replays as before: every failing
// report is quint's, its positions and the model's states quint's.
//
// GOTOGETHER_EVAL=quint (Options.Eval) has quint judge every run;
// GOTOGETHER_EVAL=shadow has quint judge every run too and fails a run
// whose verdict the evaluator gave otherwise (the nightly's guard).

// Process-wide: how many actors' traces the Go evaluator passed with no
// quint replay (a test's, to see a pass spare quint).
var evalPassed atomic.Int64

// A runVerdict is the evaluator's verdict on one actor's plan: whether it
// passes, and where it fails otherwise, in a Result's terms (call
// indices: -1 the start).
type runVerdict struct {
	pass     bool
	conforms bool   // every call taken, every expect holding
	at       int    // a call rejected (-1: the start), when it does not conform
	violated int    // the call after which an invariant first fails (-1: in the start's state), else -2
	eventual string // an eventual val false at the settle
	states   []qeval.State
}

// String is the verdict as a disagreement names it.
func (v runVerdict) String() string {
	switch {
	case !v.conforms && v.at < 0:
		return "the start is no state of the model"
	case !v.conforms:
		return fmt.Sprintf("it does not conform at call %d", v.at+1)
	case v.violated >= 0:
		return fmt.Sprintf("an invariant fails after call %d", v.violated+1)
	case v.violated == -1:
		return "an invariant fails in the start's state"
	case v.eventual != "":
		return fmt.Sprintf("eventual val %s is false at the settle", v.eventual)
	}
	return "it passes"
}

// verdictOfResult is quint's result as a runVerdict (no states).
func verdictOfResult(r *Result) runVerdict {
	v := runVerdict{conforms: r.Conforms == Pass, at: -1, violated: -2}
	if !v.conforms {
		if r.Failure != nil {
			v.at = r.Failure.Index
		}
		return v
	}
	for _, i := range r.Invariants {
		switch {
		case i.Verdict != Fail:
		case i.Eventually:
			if v.eventual == "" {
				v.eventual = strings.TrimPrefix(i.Name, "eventually ")
			}
		case i.Failure != nil && (v.violated == -2 || i.Failure.Index < v.violated):
			v.violated = i.Failure.Index
		case i.Failure == nil && v.violated == -2:
			v.violated = len(r.Calls) - 1
		}
	}
	v.pass = v.violated == -2 && v.eventual == ""
	return v
}

// agrees reports whether the evaluator's verdict ev is quint's q: the
// same pass, the same conformance and its failing call, the first state
// an invariant fails in, and whether an eventual val is false.
func (ev runVerdict) agrees(q runVerdict) bool {
	if ev.pass != q.pass || ev.conforms != q.conforms {
		return false
	}
	if !ev.conforms {
		return ev.at == q.at
	}
	return ev.violated == q.violated && (ev.eventual == "") == (q.eventual == "")
}

// runEval is how c's checks use the Go evaluator: decide (a pass is the
// evaluator's), shadow (quint judges every run too, a disagreement the
// run's error), or neither (nil).
type runEval struct {
	e      *evaluator
	shadow bool
}

// runEvaluator is how c's checks of runs use the evaluator, nil when
// quint judges them alone: under GOTOGETHER_EVAL=quint, for a search's
// candidates (quint confirms the evaluator's conclusions there), for a
// binding with hidden actions (the evaluator's search, checkHidden), and
// for a binding with no evaluator. A binding's bounded responses are
// judged on the evaluator's states of a run it passes (decide).
func (c *Checker) runEvaluator() *runEval {
	mode := c.Options.evalMode()
	if mode == "quint" || c.mode.history != nil || c.mode.quintOnly || len(c.Binding.Hidden) > 0 {
		return nil
	}
	e := evaluatorFor(c)
	if e.err != nil {
		return nil
	}
	return &runEval{e: e.replaying(), shadow: mode == "shadow"}
}

// replaying is e judging steps as quint's replay takes them
// (qeval.Model.AsReplayed): a run's evaluator, where a search's takes a
// step some value of a choice enables.
func (e *evaluator) replaying() *evaluator {
	r := *e
	r.m = e.m.AsReplayed()
	return &r
}

// judgeRun is the evaluator's verdict on plan, trace tr's (settled if it
// recorded a settle last), keeping the states with keep. An error is its
// refusal: quint judges the run.
func (e *evaluator) judgeRun(c *Checker, tr *qtrace.Trace, plan *binding.Plan, keep bool) (runVerdict, error) {
	v := runVerdict{at: -1, violated: -2}
	consts, err := e.constants(plan)
	if err != nil {
		return v, err
	}
	var record func(qeval.State)
	if keep {
		record = func(s qeval.State) { v.states = append(v.states, s) }
	}
	w, err := e.walk(c, consts, plan, 0, nil, len(c.Binding.Invariants) > 0, tr.Settled, record)
	if err != nil {
		return v, err
	}
	if hooks.TamperEval.Load() == 6 {
		v.pass, v.conforms = true, true
		return v, nil
	}
	switch {
	case w.startFailed:
		return v, nil
	case w.rejected >= 0:
		v.at = w.rejected
		return v, nil
	}
	v.conforms = true
	if w.violated >= 0 {
		v.violated = w.violated - 1
	}
	v.eventual = w.eventual
	v.pass = w.violated < 0 && w.eventual == ""
	if hooks.TamperEval.Load() == 7 && len(v.states) > 0 {
		for i := range v.states {
			v.states[i] = w.final // every state the last: an obligation it opened is met at once
		}
	}
	if t := hooks.TamperEval.Load(); t == 1 && v.pass || t == 3 && v.pass && len(plan.Calls)%2 == 1 {
		return runVerdict{at: 0, violated: -2}, nil // rejects (quint judges the run)
	}
	return v, nil
}

// passed fills res, plan's, as quint's replay fills a passing run's,
// from the evaluator's verdict: every check passed, and, unless opt says
// NoITF, the replay's ITF written from its states under opt.Dir (base
// names it, as quint's would be). false if it cannot (an ITF it could not
// write): quint judges the run.
func (e *evaluator) passed(b *binding.Binding, res *Result, plan *binding.Plan, v runVerdict, opt Options, base string) bool {
	if !opt.NoITF {
		path := filepath.Join(opt.Dir, base+"_eval.itf.json")
		if err := e.writeITF(path, plan, v.states); err != nil {
			return false
		}
		res.ITF = path
	}
	fillPass(b, res, plan)
	return true
}

// decide makes res, trace t's plan p's, the evaluator's pass v: passed's
// result, its bounded responses (the binding's within) judged on the
// evaluator's states as judgeWithin judges quint's. false, res as
// newResult left it, when it cannot (passed's) or an obligation is not
// met in it (late, never, or no verdict: a report that fails, or is not
// OK, is quint's): quint judges the run.
func (re *runEval) decide(c *Checker, t *qtrace.Trace, p *binding.Plan, v runVerdict, res *Result, opt Options, base string) bool {
	fresh := *res
	if !re.e.passed(c.Binding, res, p, v, opt, base) {
		*res = fresh
		return false
	}
	if len(c.Binding.Within) > 0 {
		probe := res.clone()
		c.judgeWithin(t, probe)
		for _, inv := range probe.Invariants {
			if inv.Within && inv.Verdict != Pass {
				*res = fresh
				return false
			}
		}
	}
	evalPassed.Add(1)
	return true
}

// clone is r with copies of what judgeWithin adds to.
func (r *Result) clone() *Result {
	c := *r
	c.Invariants = slices.Clone(r.Invariants)
	c.Issues = slices.Clone(r.Issues)
	c.withins = slices.Clone(r.withins)
	return &c
}

// fillPass fills res as a passing replay of plan: its steps, conformance
// and every check passed (the eventual vals of a settled trace).
func fillPass(b *binding.Binding, res *Result, plan *binding.Plan) {
	res.Calls, res.Steps, res.Conforms, res.Failure = plan.Calls, len(plan.Calls), Pass, nil
	if plan.Start != nil && plan.Start.Step != nil {
		res.Steps++
	}
	res.Module, res.Output, res.byEvaluator = "", "", true
	res.Invariants = nil
	for _, inv := range b.Invariants {
		res.Invariants = append(res.Invariants, InvariantResult{Name: inv, Verdict: Pass})
	}
	if res.Settled {
		for _, ev := range b.Eventually {
			res.Invariants = append(res.Invariants, InvariantResult{Name: EventuallyName(ev), Verdict: Pass, Eventually: true})
		}
	}
}

// checkPlan checks trace t's plan p into res (newResult's) with re (nil:
// quint alone): a pass the evaluator gives is the result, with no quint
// process; a plan it rejects is quint's detail replay; one it refuses,
// quint's replays as before. Under shadow quint judges every plan, and
// the second error is a disagreement with the evaluator's verdict. o is
// resolved; base names the evaluator's ITF.
func (c *Checker) checkPlan(ctx context.Context, re *runEval, t *qtrace.Trace, p *binding.Plan, res *Result, o Options) (*Result, error, error) {
	if re == nil {
		r, err := validatePlanIn(ctx, c.Binding, t, p, o, res, false)
		return r, nil, err
	}
	v, verr := re.e.judgeRun(c, t, p, !o.NoITF || re.shadow)
	if verr != nil {
		re.note(fmt.Sprintf("refused %s %s %d calls: %v", c.Binding.SpecPath(), nameOf(t.Actor), len(p.Calls), verr))
	}
	if verr == nil && v.pass && !re.shadow && re.decide(c, t, p, v, res, o, observedBase(t)) {
		return res, nil, nil
	}
	r, err := validatePlanIn(ctx, c.Binding, t, p, o, res, verr == nil && !v.pass)
	if err != nil || verr != nil || !re.shadow {
		return r, nil, err
	}
	return r, re.compare(c, t, p, v, r), nil
}

// compare is nil when quint's result q of trace t's plan p is what the
// evaluator's verdict v says: the same verdict, and for a pass the same
// result passed would fill, and the same states as quint's ITF (when it
// wrote one); else the disagreement, the shadow's error.
func (re *runEval) compare(c *Checker, t *qtrace.Trace, p *binding.Plan, v runVerdict, q *Result) (err error) {
	within := "" // the bounded responses compared, for the log
	defer func() {
		outcome := "agree"
		if err != nil {
			outcome = "DISAGREE"
		}
		re.note(fmt.Sprintf("%s %s %s %d calls, settled %v: %s%s", outcome, c.Binding.SpecPath(), nameOf(t.Actor), len(p.Calls), t.Settled, v, within))
	}()
	qv := verdictOfResult(q)
	if !v.agrees(qv) {
		return disagreement(t.Actor, v.String(), qv.String(), t.Steps)
	}
	if !v.pass {
		return nil
	}
	mine := newResult(c.Binding, t, p)
	fillPass(c.Binding, mine, p)
	if !sameAsPassed(mine, q) {
		return disagreement(t.Actor, fmt.Sprintf("passes with %s", resultLine(mine)), fmt.Sprintf("passes with %s", resultLine(q)), t.Steps)
	}
	if q.ITF == "" || len(v.states) == 0 {
		return nil
	}
	dir, err := os.MkdirTemp("", "gotogether-shadow-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "eval.itf.json")
	if err := re.e.writeITF(path, p, v.states); err != nil {
		return disagreement(t.Actor, "passes, its ITF not written ("+err.Error()+")", "passes, its ITF written", t.Steps)
	}
	if err := sameStates(path, q.ITF); err != nil {
		return disagreement(t.Actor, "passes through other states ("+err.Error()+")", "passes, its ITF "+q.ITF, t.Steps)
	}
	if len(c.Binding.Within) > 0 {
		// the bounded responses, judged on each one's states as a check
		// judges them: each obligation's verdict, where it failed, the
		// evidence the report prints, and what is absent
		mine.ITF = path
		theirs := q.clone()
		c.judgeWithin(t, mine)
		c.judgeWithin(t, theirs)
		a, b := withinText(mine), withinText(theirs)
		within = " | within: " + a
		if a != b {
			return disagreement(t.Actor, "judges its bounded responses so: "+a, "so: "+b, t.Steps)
		}
	}
	return nil
}

// withinText is what judgeWithin added to r, one line: each obligation's
// verdict, its failure's step and note, its issues, and the obligations
// not met or not judged.
func withinText(r *Result) string {
	var parts []string
	for _, i := range r.Invariants {
		if !i.Within {
			continue
		}
		p := fmt.Sprintf("%s %s", i.Name, i.Verdict)
		if f := i.Failure; f != nil {
			p += fmt.Sprintf(" at call %d: %s", f.Index+1, f.Note)
		}
		if i.cause != "" {
			p += " (" + i.cause + ")"
		}
		parts = append(parts, p)
	}
	for _, is := range r.Issues {
		if is.Kind == "within" {
			parts = append(parts, is.String())
		}
	}
	for _, o := range r.withins {
		parts = append(parts, fmt.Sprintf("%s %s due %s: %s", o.name, o.how, stamp(o.due), o.note))
	}
	if r.noVerdict != "" {
		parts = append(parts, "no verdict: "+r.noVerdict)
	}
	return strings.Join(parts, "; ")
}

// resultLine is a passing result's steps and checks, one line.
func resultLine(r *Result) string {
	parts := []string{fmt.Sprintf("%d steps", r.Steps)}
	for _, i := range r.Invariants {
		parts = append(parts, i.Name+" "+string(i.Verdict))
	}
	return strings.Join(parts, ", ")
}

// sameStates is nil when the ITF traces at a and b (the evaluator's and
// quint's) hold the same states: the same variables with equal values,
// the same action taken.
func sameStates(a, b string) error {
	x, err := itf.ReadFile(a)
	if err != nil {
		return err
	}
	y, err := itf.ReadFile(b)
	if err != nil {
		return err
	}
	if len(x.States) != len(y.States) {
		return fmt.Errorf("%d states, quint's %d", len(x.States), len(y.States))
	}
	for i := range x.States {
		s, u := x.States[i], y.States[i]
		if len(s.Vars) != len(u.Vars) {
			return fmt.Errorf("state %d: variables %v, quint's %v", i, slices.Sorted(maps.Keys(s.Vars)), slices.Sorted(maps.Keys(u.Vars)))
		}
		for k, v := range s.Vars {
			w, ok := u.Vars[k]
			if !ok || !v.Equal(w) {
				return fmt.Errorf("state %d: %s = %s, quint's %s", i, k, v, w)
			}
		}
		if s.Action != u.Action {
			return fmt.Errorf("state %d: action %s, quint's %s", i, s.Action, u.Action)
		}
	}
	return nil
}

// writeITF writes states as the ITF trace quint's replay of them would
// write: the model's variables (annotateITF then shortens their names),
// each state's values.
func (e *evaluator) writeITF(path string, plan *binding.Plan, states []qeval.State) error {
	if len(states) == 0 {
		return fmt.Errorf("no states")
	}
	type state map[string]any
	out := struct {
		Meta   map[string]any `json:"#meta"`
		Vars   []string       `json:"vars"`
		States []state        `json:"states"`
	}{Meta: map[string]any{"format": "ITF", "description": "written by gotogether's Go evaluator (internal/qeval) from the replay it passed: no quint process (GOTOGETHER_EVAL=quint has quint replay it)"},
		Vars: e.m.Vars}
	for i, s := range states {
		st := state{"#meta": map[string]any{"index": i}}
		for _, name := range e.m.Vars {
			v, err := toITF(s[name])
			if err != nil {
				return err
			}
			st[name] = v
		}
		out.States = append(out.States, st)
	}
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return annotateITF(path, &replay.Generated{Calls: plan.Calls, Start: plan.Start})
}

// toITF is an evaluator's value as an ITF value.
func toITF(v qeval.Value) (itf.Value, error) {
	elems := func(vs []qeval.Value) ([]itf.Value, error) {
		out := make([]itf.Value, len(vs))
		for i, x := range vs {
			y, err := toITF(x)
			if err != nil {
				return nil, err
			}
			out[i] = y
		}
		return out, nil
	}
	switch x := v.(type) {
	case qeval.Int:
		return itf.IntOf(int64(x)), nil
	case qeval.Bool:
		return itf.BoolOf(bool(x)), nil
	case qeval.Str:
		return itf.StrOf(string(x)), nil
	case *qeval.Set:
		es, err := elems(x.Elems)
		return itf.Value{Kind: itf.Set, Elems: es}, err
	case qeval.List:
		es, err := elems(x)
		return itf.Value{Kind: itf.List, Elems: es}, err
	case qeval.Tup:
		es, err := elems(x)
		return itf.Value{Kind: itf.Tuple, Elems: es}, err
	case *qeval.Map:
		ps := make([][2]itf.Value, len(x.Keys))
		for i := range x.Keys {
			k, err := toITF(x.Keys[i])
			if err != nil {
				return itf.Value{}, err
			}
			w, err := toITF(x.Vals[i])
			if err != nil {
				return itf.Value{}, err
			}
			ps[i] = [2]itf.Value{k, w}
		}
		return itf.Value{Kind: itf.Map, Pairs: ps}, nil
	case *qeval.Rec:
		fs := map[string]itf.Value{}
		for i, n := range x.Names {
			w, err := toITF(x.Vals[i])
			if err != nil {
				return itf.Value{}, err
			}
			fs[n] = w
		}
		return itf.Value{Kind: itf.Record, Fields: fs}, nil
	case qeval.Variant:
		w, err := toITF(x.Val)
		if err != nil {
			return itf.Value{}, err
		}
		return itf.Value{Kind: itf.Variant, Tag: x.Label, Inner: &w}, nil
	}
	return itf.Value{}, fmt.Errorf("a value ITF has no form for: %T", v)
}

// disagreement is a shadow's error for actor a's run, the evaluator's
// verdict ev against quint's q.
func disagreement(a string, ev, q string, steps []qtrace.Step) error {
	return fmt.Errorf("GOTOGETHER_EVAL=shadow: actor %s: the Go evaluator and quint disagree on its steps as checked (the evaluator: %s, quint: %s): "+
		"a bug of the evaluator (internal/qeval) or of the binding's evaluation module (binding.EvalModule); report it with this run's steps "+
		"(GOTOGETHER_KEEP keeps them), and check with GOTOGETHER_EVAL=quint meanwhile\n  %s",
		nameOf(a), ev, q, stepsString(steps))
}

// sameAsPassed reports whether quint's passing result q says what the
// evaluator's (res, filled by passed) would: its steps, its checks and
// their verdicts.
func sameAsPassed(res, q *Result) bool {
	if res.Steps != q.Steps || res.Conforms != q.Conforms || len(res.Invariants) != len(q.Invariants) {
		return false
	}
	for i, inv := range res.Invariants {
		o := q.Invariants[i]
		if inv.Name != o.Name || inv.Verdict != o.Verdict || inv.Eventually != o.Eventually {
			return false
		}
	}
	return true
}

// note appends line to the file GOTOGETHER_SHADOW_LOG names, under
// shadow: each run the evaluator and quint both judged, and each the
// evaluator refused, so a shadow job can say how much of the evaluator's
// deciding it covered (the nightly prints its counts).
func (re *runEval) note(line string) {
	path := os.Getenv("GOTOGETHER_SHADOW_LOG")
	if !re.shadow || path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write([]byte(strings.ReplaceAll(line, "\n", " ") + "\n")) // one write: lines of parallel tests stay whole
}
