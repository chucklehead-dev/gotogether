package qtest

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Model claims (I17): what the model on its own does, before any code
// runs, judged by validate.Simulate (the engine `gotogether simulate`
// prints), with the vocabulary runs' claims have:
//
//	qtest.Model(t, "hello.binding.yaml").Expect(t,
//		qtest.Proves("exactlyOnce"), qtest.NoDeadlocks(), qtest.Follows(retried))
//
// Each claim has three values and says its bound (DESIGN §65): it holds,
// it fails, or it has no verdict, which fails the test too. Proves,
// Live and NoDeadlocks need the Go evaluator's state graph within the
// binding's simulate.graphStates and graphDepth: complete (every reachable state), or, for
// Proves and NoDeadlocks, bounded by simulate.graphDepth alone (every state
// within that many steps: a bounded proof, said so). A graph the state
// bound truncated is a breadth-first prefix and proves nothing: no
// verdict, naming the bound to raise. A violation, a lasso or a deadlock
// is a witness wherever the graph found it, replayed by quint test.
// Follows is found by the evaluator's search or quint's sampling, and
// impossible only by a complete search.

// Model is the model of binding (a binding file) on its own, for claims
// about it (ModelCheck.Expect). It skips the test without quint.
func Model(t testing.TB, binding string) *ModelCheck {
	t.Helper()
	RequireQuint(t)
	return &ModelCheck{binding: binding}
}

// A ModelCheck is a model alone, its claims stated by Expect.
type ModelCheck struct {
	binding string
}

// A ModelClaim is what a test expects of a model on its own
// (ModelCheck.Expect): Proves, Refutes, Live, NoDeadlocks, Follows.
type ModelClaim interface {
	String() string
	// needs adds what the claim needs simulated to n
	needs(n *modelNeeds) error
	// judgeModel is the claim's value in rep, with what it saw and its
	// bound
	judgeModel(rep *validate.SimulateReport) (tri, string)
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

// Expect simulates the model once for every claim and fails t for each
// that does not hold, or has no verdict, saying its bound; each that
// holds is logged with its bound.
func (m *ModelCheck) Expect(t testing.TB, claims ...ModelClaim) {
	t.Helper()
	if len(claims) == 0 {
		t.Fatal("qtest.Model(...).Expect: no claims: state what the model on its own must show (Proves, Refutes, Live, NoDeadlocks, Follows)")
	}
	b, err := binding.Load(m.binding)
	if err != nil {
		t.Fatal(err)
	}
	n := &modelNeeds{b: b, invariants: []string{}, eventually: []string{}, possibles: []binding.Possible{}}
	for _, c := range claims {
		if err := c.needs(n); err != nil {
			t.Fatalf("qtest.Model(%s): %s: %v", m.binding, c, err)
		}
	}
	rep, err := validate.Simulate(m.binding, validate.SimulateOptions{Invariants: n.invariants, Eventually: n.eventually, Possibles: n.possibles, Graph: n.graph, Replay: n.graph})
	if err != nil {
		t.Fatalf("qtest.Model(%s): %v", m.binding, err)
	}
	if rep.Skipped != "" {
		t.Fatalf("qtest.Model(%s): no claim has a verdict: the model is not simulated (%s)", m.binding, rep.Skipped)
	}
	for _, c := range claims {
		switch v, why := c.judgeModel(rep); v {
		case yes:
			t.Logf("the model %s: %s", c, why)
		case no:
			t.Errorf("the model fails %s: %s", c, why)
		default:
			t.Errorf("the model has no verdict on %s: %s", c, why)
		}
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
func verdictOf(vs []validate.ModelVerdict, name string) *validate.ModelVerdict {
	for i := range vs {
		if vs[i].Name == name {
			return &vs[i]
		}
	}
	return nil
}

// calls is a counterexample's steps after init as one line.
func calls(cx *validate.Counterexample) string {
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
// cannot replay it as drawn, unknown if quint's replay fails it (the
// evaluator's error).
func witnessed(v *validate.ModelVerdict, what string) (tri, string) {
	switch {
	case v.Replayed == "replayed":
		return yes, what + " (replayed by quint test)"
	case strings.HasPrefix(v.Replayed, "not replayed: steps"):
		return yes, what + " (" + v.Replayed + ")"
	case v.Replayed != "":
		return unknown, what + ", " + v.Replayed
	}
	return yes, what
}

type proves struct{ inv string }

// Proves claims the model guarantees the invariant inv: no reachable
// state violates it, in the Go evaluator's complete state graph (a proof
// for the binding's instance) or every state within simulate.graphDepth
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

func (c proves) judgeModel(rep *validate.SimulateReport) (tri, string) {
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

func (c RefutesClaim) judgeModel(rep *validate.SimulateReport) (tri, string) {
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

func (c live) judgeModel(rep *validate.SimulateReport) (tri, string) {
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

type noDeadlocks struct{}

// NoDeadlocks claims every reachable state the step cannot leave is one
// simulate.terminal declares an end: in the complete graph, or within
// simulate.graphDepth (bounded). A deadlock fails it with the nearest.
func NoDeadlocks() ModelClaim { return noDeadlocks{} }

func (noDeadlocks) String() string { return "having no deadlock" }

func (noDeadlocks) needs(n *modelNeeds) error { n.graph = true; return nil }

func (noDeadlocks) judgeModel(rep *validate.SimulateReport) (tri, string) {
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

// Follows claims the model on its own can follow each of scenarios
// (qtest.Scenario, gristtest.Scenario): an acceptance criterion checked
// before any code runs, since a scenario the model forbids no conforming
// run can show. Each is searched by the Go evaluator from init (its
// steps' actions, arguments, and outcomes the binding passes to a
// parameter) and, if not found, sampled by quint where quint can say (a
// sequence of actions with no arguments); found, it holds with its
// witness; impossible only by a complete search; else no verdict.
//
// It is qtest.Possible(t, binding, scenarios...) as a claim, with quint's
// sampling where the evaluator does not find one (ROADMAP I17 folds
// Possible into it).
func Follows(scenarios ...Expectation) ModelClaim {
	if len(scenarios) == 0 {
		panic("qtest.Follows: no scenarios")
	}
	p := &possible{}
	for _, e := range scenarios {
		if e.scenario == nil {
			panic(fmt.Sprintf("qtest.Follows: %s is no scenario (qtest.Scenario, qtest.NamedScenario, gristtest.Scenario)", e))
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

func (p *possible) judgeModel(rep *validate.SimulateReport) (tri, string) {
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
		line := sc.String() + ": " + pv.Verdict
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
