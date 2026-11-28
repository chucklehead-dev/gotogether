package qtest

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/model"

	"github.com/chucklehead-dev/gotogether/binding"
)

// Model claims (I17): what the model on its own does, before any code
// runs, judged by model.Simulate (the engine `gotogether simulate`
// prints), with the vocabulary runs' claims have:
//
//	qtest.Model(t, "hello.binding.yaml").Expect(t,
//		qtest.Proves("exactlyOnce"), qtest.NoDeadlocks(), qtest.Possible(retried))
//
// Each claim has three values and says its bound (DESIGN §65): it holds,
// it fails, or it has no verdict, which fails the test too. Proves,
// Live and NoDeadlocks need the Go evaluator's state graph within the
// binding's simulate.search: complete (every reachable state), or, for
// Proves and NoDeadlocks, bounded by simulate.search.depth alone (every state
// within that many steps: a bounded proof, said so). A graph the state
// bound truncated is a breadth-first prefix and proves nothing: no
// verdict, naming the bound to raise. A violation, a lasso or a deadlock
// is a witness wherever the graph found it, replayed by quint test.
// Possible is found by the evaluator's search or quint's sampling, and
// impossible only by a complete search.
// AllStepActionsReachable also uses the graph, requiring one observed transition
// for each named step action, even in a partial graph; absence is a failure only
// in a complete graph.

// Model is the model of binding (a binding file) on its own, for claims
// about it (ModelCheck.Expect). Where quint is not installed it is the
// binding's compiled model's (gotogether compile, GOTOGETHER_COMPILED as
// for runs: RequireModel): the graph, the searches and the sampling are
// the Go evaluator's alone, and what needs quint (the graph's cross-check
// against quint's samples, a witness's replay by quint test) is not done,
// each claim saying so. It skips the test with neither.
func Model(t testing.TB, binding string) *ModelCheck {
	t.Helper()
	path, ok := compiledFor(t, binding, validate.Options{})
	if !ok {
		RequireQuint(t)
	}
	return &ModelCheck{binding: binding, compiled: path}
}

// A ModelCheck is a model alone, its claims stated by Expect.
type ModelCheck struct {
	binding string
	options ModelOptions
	// compiled is the compiled model it is analysed by ("": quint)
	compiled string
}

// ModelOptions select a test-local model instance and search bounds without
// changing its binding or model files. Claims still select the checks and require
// native replay of graph witnesses.
type ModelOptions struct {
	// Constants are Quint expressions keyed by declared constant name, overriding
	// both binding.constants and simulate.constants.
	Constants map[string]string
	// States and Depth override search bounds when positive. Zero inherits the
	// binding's simulate.search or the engine default; it does not clear a bound.
	// Negative bounds are rejected by Expect before loading the binding.
	States, Depth int
}

// WithOptions returns an independent check with options replacing its previous
// options. Constants are copied, so later caller edits cannot change the check.
func (m *ModelCheck) WithOptions(options ModelOptions) *ModelCheck {
	copy := *m
	copy.options = options
	copy.options.Constants = maps.Clone(options.Constants)
	return &copy
}

func (o ModelOptions) validate() error {
	for _, bound := range []struct {
		name  string
		value int
	}{{"States", o.States}, {"Depth", o.Depth}} {
		if bound.value < 0 {
			return fmt.Errorf("ModelOptions.%s=%d: want a nonnegative bound (0 inherits the binding/default)", bound.name, bound.value)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(o.Constants)) {
		if name == "" || strings.ContainsFunc(name, unicode.IsSpace) || strings.Contains(name, "=") {
			return fmt.Errorf("ModelOptions.Constants key %q: want a nonempty constant name without whitespace or '='", name)
		}
		if strings.TrimSpace(o.Constants[name]) == "" {
			return fmt.Errorf("ModelOptions.Constants[%q]: want a nonempty Quint expression", name)
		}
	}
	return nil
}

func (o ModelOptions) constants() []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(o.Constants)) {
		out = append(out, name+"="+o.Constants[name])
	}
	return out
}

// A ModelClaim is what a test expects of a model on its own
// (ModelCheck.Expect): Proves, Refutes, Live, NoDeadlocks, Inductive,
// AllStepActionsReachable, Possible.
type ModelClaim interface {
	String() string
	// needs adds what the claim needs simulated to n
	needs(n *modelNeeds) error
	// judgeModel is the claim's value in rep, with what it saw and its
	// bound
	judgeModel(rep *model.Report) (tri, string)
}

// modelNeeds are what one Expect's claims need: the invariants, eventual
// vals and possibles simulated, and whether the graph is.
type modelNeeds struct {
	b                      *binding.Binding
	invariants, eventually []string
	possibles              []binding.Possible
	graph                  bool
}

func (n *modelNeeds) invariant(name string) {
	if !slices.Contains(n.invariants, name) {
		n.invariants = append(n.invariants, name)
	}
}

func (n *modelNeeds) eventual(name string) {
	if !slices.Contains(n.eventually, name) {
		n.eventually = append(n.eventually, name)
	}
}

// Expect simulates the model once for all claims and fails t for each
// that does not hold, or has no verdict, saying its bound; each that
// holds is logged with its bound.
func (m *ModelCheck) Expect(t testing.TB, claims ...ModelClaim) {
	t.Helper()
	if len(claims) == 0 {
		t.Fatal("qtest.Model(...).Expect: no claims: state what the model on its own must show (Proves, Refutes, Live, NoDeadlocks, AllStepActionsReachable, Possible, Inductive)")
	}
	if err := m.options.validate(); err != nil {
		t.Fatalf("qtest.Model(%s): %v", m.binding, err)
	}
	if err := t.Context().Err(); err != nil {
		t.Fatalf("qtest.Model(%s): %v", m.binding, err)
	}
	b, err := binding.Load(m.binding)
	if err != nil {
		t.Fatal(err)
	}
	n := &modelNeeds{b: b, invariants: []string{}, eventually: []string{}, possibles: []binding.Possible{}}
	var inductive []model.InductiveRequest
	others := 0
	for _, c := range claims {
		if ic, ok := c.(InductiveClaim); ok {
			if err := ic.check(b); err != nil {
				t.Fatalf("qtest.Model(%s): %s: %v", m.binding, c, err)
			}
			inductive = append(inductive, ic.request())
			continue
		}
		others++
		if err := c.needs(n); err != nil {
			t.Fatalf("qtest.Model(%s): %s: %v", m.binding, c, err)
		}
	}
	rep := &model.Report{Binding: m.binding}
	if others > 0 {
		var err error
		rep, err = model.Simulate(t.Context(), m.binding, model.SimulateOptions{
			Invariants: n.invariants, Eventually: n.eventually, Possibles: n.possibles,
			Graph: n.graph, Replay: n.graph, Constants: m.options.constants(),
			States: m.options.States, Depth: m.options.Depth, Compiled: m.compiled,
		})
		if err != nil {
			t.Fatalf("qtest.Model(%s): %v", m.binding, err)
		}
		if rep.Skipped != "" {
			t.Fatalf("qtest.Model(%s): no claim has a verdict: the model is not simulated (%s)", m.binding, rep.Skipped)
		}
	}
	if len(inductive) > 0 {
		// Veil proves for every value of the constants the proof module
		// leaves open: the binding's constants and bounds are the vacuity
		// search's alone
		irep, err := model.Simulate(t.Context(), m.binding, model.SimulateOptions{
			Inductive: inductive, InductiveOut: inductiveKeep(t), Constants: m.options.constants(),
			States: m.options.States, Depth: m.options.Depth,
		})
		if err != nil {
			t.Fatalf("qtest.Model(%s): %v", m.binding, err)
		}
		rep.Inductive = irep.Inductive
		for _, nd := range irep.NotDone {
			t.Logf("gotogether: not done: %s", nd)
		}
	}
	if rep.Compiled != "" {
		t.Logf("gotogether: the model of %s analysed by its compiled model %s, the Go evaluator alone (no quint here, or %s=1)", m.binding, rep.Compiled, compiledEnv)
		for _, n := range rep.NotDone {
			t.Logf("gotogether: not done: %s", n)
		}
	}
	var skipped []string
	for _, c := range claims {
		if ic, ok := c.(InductiveClaim); ok {
			if why, skip := ic.skipped(rep); skip {
				t.Logf("the model has no verdict on %s, and the test does not fail of it: %s", c, why)
				skipped = append(skipped, c.String()+": "+why)
				continue
			}
		}
		switch v, why := c.judgeModel(rep); v {
		case yes:
			t.Logf("the model %s: %s", c, why)
		case no:
			t.Errorf("the model fails %s: %s", c, why)
		default:
			t.Errorf("the model has no verdict on %s: %s", c, why)
		}
	}
	if len(skipped) > 0 && !t.Failed() {
		// refused by Veil, or no Veil here: never a failure (GOTOGETHER_STRICT
		// does not make one: CI installs no Veil)
		t.Skip("qtest.Model: " + strings.Join(skipped, "; "))
	}
}

// knownName fails a name that is neither an invariant nor an eventual
// val of the binding: a misspelled claim would hold of nothing.
func knownName(b *binding.Binding, name string, eventual bool) error {
	switch {
	case eventual && !slices.Contains(b.Eventually, name):
		return fmt.Errorf("%s is no eventual val of the binding (eventually: %s)", name, strings.Join(b.Eventually, ", "))
	case !eventual && !slices.Contains(b.Invariants, name):
		return fmt.Errorf("%s is no invariant of the binding (invariants: %s)", name, strings.Join(b.Invariants, ", "))
	}
	return nil
}

// verdictOf is the verdict named name among vs, nil if none.
func verdictOf(vs []model.Verdict, name string) *model.Verdict {
	for i := range vs {
		if vs[i].Name == name {
			return &vs[i]
		}
	}
	return nil
}

// calls is a counterexample's steps after init as one line.
func calls(cx *model.Counterexample) string {
	if cx == nil {
		return ""
	}
	var cs []string
	for _, st := range cx.Steps[1:] {
		cs = append(cs, st.Call)
	}
	if len(cs) == 0 {
		return "init"
	}
	return strings.Join(cs, ", ")
}

// witnessed is a verdict's witness's value: yes if quint replayed it or
// cannot replay it as drawn, or if there is no quint to replay it (a
// compiled model's: the witness is the evaluator's alone, and the claim
// says the replay was not done); unknown if quint's replay fails it (the
// evaluator's error).
func witnessed(v *model.Verdict, what string) (tri, string) {
	switch {
	case v.Replayed == "replayed":
		return yes, what + " (replayed by quint test)"
	case strings.HasPrefix(v.Replayed, "not replayed: steps"), strings.HasPrefix(v.Replayed, "not done: "):
		return yes, what + " (" + v.Replayed + ")"
	case v.Replayed != "":
		return unknown, what + ", " + v.Replayed
	}
	return yes, what
}

type proves struct{ inv string }

// Proves claims the model guarantees the invariant inv: no reachable
// state violates it, in the Go evaluator's complete state graph (a proof
// for the binding's instance) or every state within simulate.search.depth
// (a bounded proof). A violation fails it with the shortest
// counterexample; a graph the state bound cut has no verdict.
func Proves(inv string) ModelClaim { return proves{inv} }

func (c proves) String() string { return fmt.Sprintf("proving %s", c.inv) }

func (c proves) needs(n *modelNeeds) error {
	if slices.Contains(n.b.Eventually, c.inv) {
		return fmt.Errorf("%s is an eventual val: claim it with qtest.Live", c.inv)
	}
	if err := knownName(n.b, c.inv, false); err != nil {
		return err
	}
	n.invariant(c.inv)
	n.graph = true
	return nil
}

func (c proves) judgeModel(rep *model.Report) (tri, string) {
	v := verdictOf(rep.Invariants, c.inv)
	switch {
	case v == nil:
		return unknown, "not checked"
	case v.Error != "":
		return no, "evaluating it fails in a reachable state (" + v.Error + "): the model's own error, after " + calls(v.Counterexample)
	case v.Violated:
		return no, fmt.Sprintf("a state %d steps from init violates it: %s (replay: run r = %s)", len(v.Counterexample.Steps)-1, calls(v.Counterexample), v.Counterexample.Replay)
	case v.Unchecked != "":
		return unknown, "the Go evaluator cannot evaluate it (" + v.Unchecked + "): gotogether simulate without -graph samples it with quint"
	case v.Held && v.Bound.Proves():
		return yes, "no state violates it in " + v.Bound.String()
	}
	return unknown, "no state violates it in " + v.Bound.String()
}

type refutes struct {
	name string
	most int // the witness's steps at most, plus one (0: any)
}

// Refutes claims the model allows name to fail, so checking the code
// against it means something: a reachable state violating the invariant
// name, or, for an eventual val, a fair cycle keeping it false forever (a
// lasso). Its witness is replayed by quint test. A complete graph with
// none fails it (the model guarantees it: Proves or Live is the claim); a
// truncated one has no verdict.
func Refutes(name string) RefutesClaim { return RefutesClaim{refutes{name: name}} }

// A RefutesClaim is Refutes', which InAtMost bounds.
type RefutesClaim struct{ r refutes }

// InAtMost claims the witness is at most n steps from init: the shortest
// counterexample's, or a lasso's path to its cycle.
func (c RefutesClaim) InAtMost(n int) RefutesClaim {
	if n < 0 {
		panic("qtest.Refutes(...).InAtMost: a negative number of steps")
	}
	c.r.most = n + 1
	return c
}

func (c RefutesClaim) String() string {
	s := fmt.Sprintf("refuting %s", c.r.name)
	if c.r.most > 0 {
		s += fmt.Sprintf(" within %d steps", c.r.most-1)
	}
	return s
}

func (c RefutesClaim) needs(n *modelNeeds) error {
	switch {
	case slices.Contains(n.b.Eventually, c.r.name):
		n.eventual(c.r.name)
	case slices.Contains(n.b.Invariants, c.r.name):
		n.invariant(c.r.name)
	default:
		return fmt.Errorf("%s is no invariant (%s) nor eventual val (%s) of the binding", c.r.name, strings.Join(n.b.Invariants, ", "), strings.Join(n.b.Eventually, ", "))
	}
	n.graph = true
	return nil
}

func (c RefutesClaim) judgeModel(rep *model.Report) (tri, string) {
	eventual := verdictOf(rep.Eventually, c.r.name) != nil
	vs := rep.Invariants
	if eventual {
		vs = rep.Eventually
	}
	v := verdictOf(vs, c.r.name)
	if v == nil {
		return unknown, "not checked"
	}
	if v.Error != "" {
		return unknown, "evaluating it fails in a reachable state (" + v.Error + "): the model's own error"
	}
	if v.Violated {
		var what string
		var steps int
		if eventual {
			steps = len(v.Lasso.Prefix.Steps) - 1
			what = fmt.Sprintf("a fair cycle keeps it false forever after %d steps: %s, then a cycle", steps, calls(v.Lasso.Prefix))
		} else {
			steps = len(v.Counterexample.Steps) - 1
			what = fmt.Sprintf("a state %d steps from init violates it: %s", steps, calls(v.Counterexample))
		}
		if c.r.most > 0 && steps > c.r.most-1 {
			return no, what + fmt.Sprintf(", more than %d (the nearest)", c.r.most-1)
		}
		return witnessed(v, what)
	}
	if v.Unchecked != "" {
		return unknown, "the Go evaluator cannot evaluate it (" + v.Unchecked + ")"
	}
	if v.Bound.Kind == "complete" || !eventual && v.Bound.Proves() {
		return no, "the model guarantees it: nothing refutes it in " + v.Bound.String() + " (checking the code against it proves nothing)"
	}
	return unknown, "nothing refutes it in " + v.Bound.String()
}

type live struct{ val string }

// Live claims the model drives the eventual val itself: no fair cycle of
// its complete state graph keeps it false (every action weakly fair but
// simulate.unfair's and simulate.stops'; DESIGN §62), so it is true
// infinitely often in every fair behaviour of the instance. A lasso fails
// it; a graph any bound cut has no verdict, since a cycle may close past
// it.
func Live(val string) ModelClaim { return live{val} }

func (c live) String() string { return fmt.Sprintf("driving %s (live)", c.val) }

func (c live) needs(n *modelNeeds) error {
	if err := knownName(n.b, c.val, true); err != nil {
		return err
	}
	n.eventual(c.val)
	n.graph = true
	return nil
}

func (c live) judgeModel(rep *model.Report) (tri, string) {
	v := verdictOf(rep.Eventually, c.val)
	switch {
	case v == nil:
		return unknown, "not checked"
	case v.Error != "":
		return no, "evaluating it fails in a reachable state (" + v.Error + "): the model's own error"
	case v.Violated:
		return no, fmt.Sprintf("a fair cycle keeps it false forever after %s (replay: run r = %s)", calls(v.Lasso.Prefix), v.Lasso.Replay)
	case v.Unchecked != "":
		return unknown, "the Go evaluator cannot evaluate it (" + v.Unchecked + ")"
	case v.Held && v.Bound.Kind == "complete":
		return yes, "no fair cycle keeps it false in " + v.Bound.String()
	}
	return unknown, "no fair cycle keeps it false in " + v.Bound.String() + ", which proves nothing of the cycles past it"
}

type allStepActionsReachable struct{}

// AllStepActionsReachable claims every named action the model's step branches
// to has an observed transition in the Go evaluator's graph. This is action-name
// reachability, not coverage of argument values or distinct syntactic branches
// calling the same action, and does not claim native replay of each transition.
// Binding actions outside step and model helpers are not included.
//
// Observing every step action holds even in a partial graph. A missing action
// fails only for a complete graph; otherwise it has no verdict. No graph, no
// named step actions, or evaluator disagreement also has no verdict.
func AllStepActionsReachable() ModelClaim { return allStepActionsReachable{} }

func (allStepActionsReachable) String() string { return "reaching every step action" }

func (allStepActionsReachable) needs(n *modelNeeds) error { n.graph = true; return nil }

func (allStepActionsReachable) judgeModel(rep *model.Report) (tri, string) {
	reach := rep.StepActionReachability()
	switch reach.Status {
	case "reachable":
		return yes, reach.Reason
	case "unreachable":
		return no, reach.Reason
	default:
		return unknown, reach.Reason
	}
}

type noDeadlocks struct{}

// NoDeadlocks claims every reachable state the step cannot leave is one
// simulate.terminal declares an end: in the complete graph, or within
// simulate.search.depth (bounded). A deadlock fails it with the nearest.
func NoDeadlocks() ModelClaim { return noDeadlocks{} }

func (noDeadlocks) String() string { return "having no deadlock" }

func (noDeadlocks) needs(n *modelNeeds) error { n.graph = true; return nil }

func (noDeadlocks) judgeModel(rep *model.Report) (tri, string) {
	v := rep.Deadlocks
	switch {
	case v == nil:
		return unknown, "no graph"
	case v.Violated:
		return no, fmt.Sprintf("%d reachable states the step cannot leave where simulate.terminal does not hold; the nearest after %s", rep.Graph.Deadlocks, calls(v.Counterexample))
	case v.Bound.Proves():
		return yes, fmt.Sprintf("none in %s (%d terminal)", v.Bound, rep.Graph.Terminals)
	}
	return unknown, "none in " + v.Bound.String()
}

type possible struct {
	scenarios []*scenario
	// at are the scenarios' possibles' indices in the report; errs why
	// one is no search
	at   []int
	errs []error
}

// Possible claims the model on its own can follow each of scenarios
// (qtest.Scenario, gristtest.Scenario): an acceptance criterion checked
// before any code runs, since a scenario the model forbids no conforming
// run can show. Each is searched by the Go evaluator from init (its
// steps' actions, arguments, and outcomes the binding passes to a
// parameter) and, if not found, sampled by quint where quint can say (a
// sequence of actions with no arguments); found, it holds with its
// witness; impossible only by a complete search; else no verdict.
//
// It replaces qtest.Possible(t, binding, scenarios...), which failed "not
// searched" where quint would have sampled.
func Possible(scenarios ...Expectation) ModelClaim {
	if len(scenarios) == 0 {
		panic("qtest.Possible: no scenarios")
	}
	p := &possible{}
	for _, e := range scenarios {
		if e.scenario == nil {
			panic(fmt.Sprintf("qtest.Possible: %s is no scenario (qtest.Scenario, qtest.NamedScenario, gristtest.Scenario)", e))
		}
		p.scenarios = append(p.scenarios, e.scenario)
	}
	return p
}

func (p *possible) String() string {
	var d []string
	for _, sc := range p.scenarios {
		d = append(d, sc.String())
	}
	return "able to follow " + strings.Join(d, " and ")
}

func (p *possible) needs(n *modelNeeds) error {
	p.at, p.errs = make([]int, len(p.scenarios)), make([]error, len(p.scenarios))
	for i, sc := range p.scenarios {
		bp, err := scenarioAsPossible(n.b, sc)
		p.at[i], p.errs[i] = -1, err
		if err == nil {
			p.at[i] = len(n.possibles)
			n.possibles = append(n.possibles, bp)
		}
	}
	return nil
}

func (p *possible) judgeModel(rep *model.Report) (tri, string) {
	v := yes
	// the claim's value: no if one is impossible, else unknown if one has
	// no verdict
	open := func() {
		if v == yes {
			v = unknown
		}
	}
	var lines []string
	for i, sc := range p.scenarios {
		if p.errs[i] != nil {
			open()
			lines = append(lines, fmt.Sprintf("%s: not searched: %v", sc, p.errs[i]))
			continue
		}
		if p.at[i] >= len(rep.Possibles) {
			open()
			lines = append(lines, fmt.Sprintf("%s: not searched", sc))
			continue
		}
		pv := rep.Possibles[p.at[i]]
		line := pv.Verdict
		if len(p.scenarios) > 1 {
			line = sc.String() + ": " + line // the claim's name says one
		}
		switch {
		case pv.Found:
			lines = append(lines, line)
		case pv.Impossible:
			v = no
			lines = append(lines, line)
		default:
			open()
			lines = append(lines, line)
		}
	}
	if len(lines) == 1 {
		return v, lines[0]
	}
	return v, "\n  " + strings.Join(lines, "\n  ")
}

// An InductiveClaim is Inductive's, which With gives lemmas.
type InductiveClaim struct {
	inv    string
	lemmas []string
	given  bool
}

// Inductive claims the holds invariant inv is proved by induction with
// quint's Veil backend, for every value of the constants the proof module
// leaves open (an instance module's fixed ones are its own; DESIGN §116):
// inv and its lemmas (With, else the binding's simulate.inductive.lemmas)
// are an inductive invariant of simulate.inductive.module (else the
// binding's module), init establishing it and every action of step
// preserving it. It is no replacement for Proves, which checks the
// instance the code is checked against: claim both.
//
// A counterexample to induction has no verdict, which fails the test
// (inv may still hold: a lemma is missing), with both states, the action
// and the kept ITF ($GOTOGETHER_KEEP/<test>, else a temporary directory),
// or Veil's reason it could not decode the states. A model Veil refuses
// (outside its fragment, its reason given) and a machine with no quint ≥
// 0.35 or no Veil (`quint veil status`; how to install it given) leave it
// not done: logged, and the test skipped if nothing else failed, never
// failed. A quint that is there but does not answer `quint --version` in
// time (a loaded machine; GOTOGETHER_QUINT_PROBE_TIMEOUT raises the bound)
// fails the test: it is no missing quint. A proof that may be vacuous (a lemma
// negating inv's antecedent, a settle flag step never sets, an antecedent
// the instance never reaches) is logged as a warning.
func Inductive(inv string) InductiveClaim { return InductiveClaim{inv: inv} }

// With gives the claim its lemmas, vals of the proof module conjoined
// with the invariant as its inductive invariant, in place of the
// binding's; With() with none proves the invariant alone.
func (c InductiveClaim) With(lemmas ...string) InductiveClaim {
	c.lemmas, c.given = append([]string{}, lemmas...), true
	return c
}

func (c InductiveClaim) String() string {
	if len(c.lemmas) > 0 {
		return fmt.Sprintf("proving %s by induction with %s", c.inv, strings.Join(c.lemmas, ", "))
	}
	return fmt.Sprintf("proving %s by induction", c.inv)
}

func (c InductiveClaim) check(b *binding.Binding) error {
	if slices.Contains(b.Eventually, c.inv) {
		return fmt.Errorf("%s is an eventual val: induction proves invariants (claim it with qtest.Live)", c.inv)
	}
	if err := knownName(b, c.inv, false); err != nil {
		return err
	}
	if b.Simulate == nil || !slices.Contains(b.Simulate.Holds, c.inv) {
		return fmt.Errorf("%s is not in the binding's simulate.holds: induction proves what the model guarantees; list it there", c.inv)
	}
	return nil
}

func (c InductiveClaim) request() model.InductiveRequest {
	r := model.InductiveRequest{Name: c.inv}
	if c.given {
		r.Lemmas = slices.Clone(c.lemmas)
	}
	return r
}

// needs is never called: Expect proves inductive claims on their own.
func (c InductiveClaim) needs(*modelNeeds) error { return nil }

func (c InductiveClaim) verdict(rep *model.Report) *model.InductiveVerdict {
	for i := range rep.Inductive {
		if rep.Inductive[i].Name == c.inv {
			return &rep.Inductive[i]
		}
	}
	return nil
}

// skipped is why the claim is not done (refused, no Veil), if it is not.
func (c InductiveClaim) skipped(rep *model.Report) (string, bool) {
	v := c.verdict(rep)
	switch {
	case v == nil:
		return "", false
	case v.Outcome == model.InductiveRefused:
		return "refused by Veil, not in its fragment: " + v.Reason + " (gotogether lint -veil names the idioms and their rewrites)", true
	case v.Outcome == model.InductiveNotDone:
		return "not done: " + v.Reason, true
	}
	return "", false
}

func (c InductiveClaim) judgeModel(rep *model.Report) (tri, string) {
	v := c.verdict(rep)
	if v == nil {
		return unknown, "not proved"
	}
	text := strings.TrimSpace(model.InductiveText(*v))
	if v.Outcome == model.InductiveProved {
		return yes, text
	}
	return unknown, text
}

// inductiveKeep is where a test's counterexamples to induction are kept:
// $GOTOGETHER_KEEP/<test>, else "" (a temporary directory, named).
func inductiveKeep(t testing.TB) string {
	if dir := os.Getenv("GOTOGETHER_KEEP"); dir != "" {
		return filepath.Join(dir, strings.ReplaceAll(t.Name(), "/", "_"))
	}
	return ""
}
