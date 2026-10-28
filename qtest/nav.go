package qtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The run navigator (P5). A node is a prefix of a run's choices: a
// schedule in Exhaust's syntax (the choices made otherwise than nominal,
// choice:option) and how many choices it fixes (@k), every choice before
// k made as the schedule says and nominal otherwise. Its branches are
// the next choice's options, named as qenv names them (a fault kind's
// declared options, the runnable tasks of a scheduling choice, the
// nemeses enabled); a child is the replay with that option forced and
// every later choice nominal, stopped (for what the node shows) at the
// next choice: the steps recorded so far, Sim.Tasks, the model's state
// after them (the Go evaluator's), and the verdict of the whole run that
// node makes (nominal from there). It is stateless, as Exhaust is: a
// node is replayed from the start (milliseconds), so a system must be
// deterministic (the exploration's first-seed-twice check holds a node
// to it too: a node replays its base run's choices or says where it
// diverged).
//
// It runs inside a test binary: GOTOGETHER_RUN names the run (as a
// failing run's report does) and GOTOGETHER_NAV the node, and the
// exploration that GOTOGETHER_RUN names navigates instead of exploring
// (`gotogether nav -run 'TestX#2:seed=17'` sets both, runs the test and
// prints the node). GOTOGETHER_NAV is "start" (the run's first choice),
// "fail" (the last choice before the failing run's failing step was
// recorded), "@k" (the run's own first k choices) or "<schedule>@k";
// GOTOGETHER_NAV_FOLLOW takes options from there in turn ("1,0,2");
// GOTOGETHER_NAV_OUT is the file the node's JSON goes to (else the
// test's log); GOTOGETHER_NAV_SAVE=1 saves the node's run to the
// exploration's corpus.

// navigating reports whether GOTOGETHER_NAV asks x (run alone) to
// navigate.
func (x *Exploration) navigating() bool {
	_, ok := os.LookupEnv("GOTOGETHER_NAV")
	return ok && x.alone != nil
}

// navChooser makes a node's choices (a forcing chooser's: the schedule,
// nominal otherwise) and snapshots the run at choice at: what the node
// shows of it.
type navChooser struct {
	f     *forcing
	at    int
	sim   *qenv.Sim
	steps func() []qtrace.Step

	reached bool
	kind    string
	n       int
	options []string
	before  []qtrace.Step
	tasks   string
	elapsed string
}

// Choose implements qenv.Chooser.
func (c *navChooser) Choose(kind string, n int) int {
	if c.f.i == c.at && !c.reached {
		c.reached, c.kind, c.n = true, kind, n
		if c.sim != nil {
			c.options, c.tasks, c.elapsed = c.sim.ChoiceOptions(kind), c.sim.Tasks(), c.sim.Elapsed().String()
		}
		if c.steps != nil {
			c.before = c.steps()
		}
	}
	return c.f.Choose(kind, n)
}

// ObserveSim is how qenv.New hands the navigator the simulation it
// chooses for (its choice's options, its tasks).
func (c *navChooser) ObserveSim(s *qenv.Sim) { c.sim = s }

// nodeSpec is a node: its deviations and how many choices it fixes.
type nodeSpec struct {
	ds []deviation
	at int
}

func (s nodeSpec) id() string { return schedule(s.ds) + "@" + strconv.Itoa(s.at) }

// child is the node after taking option o at s.
func (s nodeSpec) child(o int) nodeSpec {
	ds := slices.Clone(s.ds)
	if o > 0 {
		ds = append(ds, deviation{s.at, o})
	}
	return nodeSpec{ds, s.at + 1}
}

// fixed are base's deviations before choice at.
func fixed(base []deviation, at int) []deviation {
	var out []deviation
	for _, d := range base {
		if d.at < at {
			out = append(out, d)
		}
	}
	return out
}

// navigate is the exploration's navigation: its one planned run (the
// run alone GOTOGETHER_RUN names) as the base, the node GOTOGETHER_NAV
// names from it, written where GOTOGETHER_NAV_OUT says.
func (e *engine) navigate(sources []source) {
	t, x := e.t, e.x
	t.Helper()
	var base planned
	for _, s := range sources {
		if b := s.next(nil); len(b) > 0 {
			base = b[0]
			break
		}
	}
	if base.chooser == nil {
		t.Fatalf("gotogether: GOTOGETHER_NAV: %s planned no run to navigate", x.ref())
	}
	r0 := e.one(base.chooser())
	if r0 == nil {
		t.FailNow()
	}
	r0.origin = base.origin
	node, err := e.navNode(r0)
	if err != nil {
		t.Fatalf("gotogether: GOTOGETHER_NAV: %v", err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(node); err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv("GOTOGETHER_NAV_OUT"); out != "" {
		if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Logf("gotogether: node of %s:\n%s", x.ref(), buf.String())
	}
}

// baseSchedule is r's choices as a schedule (its choices made otherwise
// than nominal), and whether it drew entropy (which a schedule does not
// replay).
func baseSchedule(r *Run) ([]deviation, []qenv.Choice, bool) {
	var ds []deviation
	var cs []qenv.Choice
	entropy := false
	for _, c := range r.Choices {
		if c.Kind == "entropy" {
			entropy = true
			continue
		}
		if c.Value != 0 {
			ds = append(ds, deviation{len(cs), c.Value})
		}
		cs = append(cs, c)
	}
	return ds, cs, entropy
}

// navNode is the node GOTOGETHER_NAV names, from base run r0.
func (e *engine) navNode(r0 *Run) (*validate.RunNode, error) {
	t, x := e.t, e.x
	ds, cs, entropy := baseSchedule(r0)
	var spec nodeSpec
	switch v := os.Getenv("GOTOGETHER_NAV"); {
	case v == "" || v == "start":
		spec = nodeSpec{nil, 0}
	case v == "fail":
		k, err := e.failingChoice(r0, cs)
		if err != nil {
			return nil, err
		}
		spec = nodeSpec{fixed(ds, k), k}
	case strings.HasPrefix(v, "@"):
		k, err := strconv.Atoi(v[1:])
		if err != nil || k < 0 {
			return nil, fmt.Errorf("GOTOGETHER_NAV=%q: want start, fail, @<k> or <schedule>@<k>", v)
		}
		spec = nodeSpec{fixed(ds, k), k}
	default:
		sched, at, ok := strings.Cut(v, "@")
		k, err := strconv.Atoi(at)
		if !ok || err != nil || k < 0 {
			return nil, fmt.Errorf("GOTOGETHER_NAV=%q: want start, fail, @<k> or <schedule>@<k> (a node's id)", v)
		}
		d, err := parseSchedule(sched)
		if err != nil {
			return nil, err
		}
		spec = nodeSpec{fixed(d, k), k}
	}
	for p := range strings.SplitSeq(os.Getenv("GOTOGETHER_NAV_FOLLOW"), ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		o, err := strconv.Atoi(p)
		if err != nil || o < 0 {
			return nil, fmt.Errorf("GOTOGETHER_NAV_FOLLOW %q: want options, comma-separated (0 nominal)", p)
		}
		spec = spec.child(o)
	}
	nc := &navChooser{f: &forcing{ds: spec.ds}, at: spec.at}
	r := e.one(nc)
	if r == nil {
		t.FailNow()
	}
	r.origin = origin{kind: "schedule", value: schedule(spec.ds)}
	node := &validate.RunNode{Run: x.ref() + ":" + r0.ID(), ID: spec.id(), Schedule: schedule(spec.ds), At: spec.at, Branches: []validate.RunBranch{}, Steps: []string{}}
	if entropy {
		node.Notes = append(node.Notes, "the base run drew entropy (qenv.EntropyOf), which a node's schedule does not replay: a node may diverge from it")
	}
	if r.Err != nil {
		node.Notes = append(node.Notes, "the node's run did not run to its end: "+r.Err.Error())
	}
	// a node of the base's own choices replays them: the system is
	// deterministic, or the node says where it is not
	if slices.Equal(spec.ds, fixed(ds, spec.at)) {
		for i := 0; i < spec.at && i < len(cs); i++ {
			if i >= len(r.Choices) || r.Choices[i].Kind != cs[i].Kind || r.Choices[i].N != cs[i].N {
				node.Notes = append(node.Notes, fmt.Sprintf("the node's replay diverged from the base run at choice %d: the run is not deterministic, or draws entropy", i))
				break
			}
		}
	}
	if nc.reached {
		taken := 0
		if spec.at < len(cs) && slices.Equal(spec.ds, fixed(ds, spec.at)) {
			taken = cs[spec.at].Value
		}
		node.Choice = &validate.RunChoice{Kind: nc.kind, N: nc.n, Options: nc.options, Taken: taken}
		for o := range nc.n {
			b := validate.RunBranch{Option: o, Child: spec.child(o).id()}
			if o < len(nc.options) {
				b.Name = nc.options[o]
			}
			node.Branches = append(node.Branches, b)
		}
		node.Elapsed = nc.elapsed
		if nc.tasks != "" {
			node.Tasks = strings.Split(nc.tasks, "\n")
		}
	} else {
		nc.before = r.Steps
		node.Notes = append(node.Notes, fmt.Sprintf("the run made %d choices: none at %d", len(r.Choices), spec.at))
	}
	for _, s := range nc.before {
		node.Steps = append(node.Steps, s.String())
	}
	cc, inst, _ := e.checker()
	node.Model = actorStates(cc, nc.before)
	judge(t, cc, inst, e.c.searchOptions(), []*Run{r}, func(*Run) string { return "the node's run " + node.ID })
	node.Verdict = "pass"
	if !r.Report.OK() {
		node.Verdict = r.Report.Reason()
	}
	if os.Getenv("GOTOGETHER_NAV_SAVE") == "1" {
		if x.corpus == "" {
			node.Notes = append(node.Notes, "not saved: the exploration has no corpus (ExploreConfig.Corpus)")
		} else if p, err := x.save(r); err != nil {
			node.Notes = append(node.Notes, "not saved: "+err.Error())
		} else {
			node.Saved = p
		}
	}
	return node, nil
}

// checker is the exploration's instance checker, as finish makes it.
func (e *engine) checker() (*validate.Checker, string, map[string]string) {
	o := e.c.Options
	o.Dir, o.NoITF = filepath.Join(e.x.dir, "quint"), true
	return instanceChecker(e.t, e.binding, o, e.c)
}

// failingChoice is the last choice r0 made before its failing step was
// recorded (index among cs, its choices but entropy): the decision
// nearest the failure. A run that passes has none.
func (e *engine) failingChoice(r0 *Run, cs []qenv.Choice) (int, error) {
	cc, inst, _ := e.checker()
	r := *r0
	judge(e.t, cc, inst, e.c.searchOptions(), []*Run{&r}, func(*Run) string { return "the run" })
	if r.Report.OK() {
		return 0, fmt.Errorf("%s passes: there is no failing step to open (GOTOGETHER_NAV=start opens its first choice)", e.x.nameOf(r0))
	}
	pos := len(r0.Steps)
	for _, a := range r.Report.Actors {
		if f := a.Failure; f != nil && f.Step != nil {
			for i, s := range r0.Steps {
				if s.Recorder == f.Step.Recorder && s.Seq == f.Step.Seq {
					pos = min(pos, i)
				}
			}
		}
	}
	k := 0
	for i, c := range cs {
		if c.Step <= pos {
			k = i
		}
	}
	return k, nil
}

// actorStates are each actor's model state after steps, by the Go
// evaluator, or why there is none.
func actorStates(cc *validate.Checker, steps []qtrace.Step) []validate.ActorState {
	traces, _ := cc.Binding.Reconstruct(steps)
	var out []validate.ActorState
	for _, tr := range traces {
		as := validate.ActorState{Actor: tr.Actor}
		n, err := cc.Nav(validate.NavStart{Steps: steps, Actor: tr.Actor, At: -1})
		if err == nil {
			var nd *validate.Node
			if nd, err = n.Node(); err == nil {
				as.State = nd.State
			}
		}
		if err != nil {
			as.Error = err.Error()
		}
		out = append(out, as)
	}
	return out
}

// navCommand is how a failing run's report opens its failing node.
func (x *Exploration) navCommand(r *Run) string {
	return fmt.Sprintf("gotogether nav -run '%s:%s' -at fail (in the test's package directory; -json for agents)", x.ref(), r.ID())
}

// Unwrap implements qenv.Wrapper: the forcing chooser's error is the
// run's (chooserErr).
func (c *navChooser) Unwrap() qenv.Chooser { return c.f }
