package qtest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"

	"github.com/chucklehead-dev/gotogether/internal/overlay"
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
// exploration's corpus; GOTOGETHER_NAV_FOLD=1 folds the node's linear
// stretch (fold).
//
// GOTOGETHER_NAV=serve makes the test binary a session (serve): it runs
// the base run (twice: the first-seed-twice check), then answers
// navproto.Request lines with navproto.Response lines until its
// input ends, each node a replay (milliseconds, the verdicts cached by
// their steps) instead of a go test process (seconds).
//
// Folding: a node asked to fold advances through the choices that are no
// decision before the next that is: those of one option, and those made
// nominally where the base run made them nominally too (on the base
// run's path: the node's choices so far are the base's), stopping at
// the base run's next choice made otherwise, or at the run's end (off
// the base path every later choice is nominal). It says how many it
// folded (RunNode.Folded).

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

	// fold: advance at through the choices that are no decision
	// (foldable), counting them in folded; base is the base run's
	// choices, onPath whether the node's choices so far are its
	fold   bool
	base   []qenv.Choice
	onPath bool
	folded int

	reached bool
	kind    string
	n       int
	options []string
	before  []qtrace.Step
	tasks   string
	elapsed string
	// path are the choices before the node, each its kind and the option
	// taken by name where the simulation names it, for the node's title
	path []pathChoice
}

// a pathChoice is a choice before a run node: its kind, and the option
// taken (its name, when the simulation gave one)
type pathChoice struct {
	kind, name string
	value      int
}

// Choose implements qenv.Chooser.
func (c *navChooser) Choose(kind string, n int) int {
	if c.f.i == c.at && !c.reached && c.fold && c.foldable(c.at, n) {
		c.at++
		c.folded++
	} else if c.f.i == c.at && !c.reached {
		c.reached, c.kind, c.n = true, kind, n
		if c.sim != nil {
			c.options, c.tasks, c.elapsed = c.sim.ChoiceOptions(kind), c.sim.Tasks(), c.sim.Elapsed().String()
		}
		if c.steps != nil {
			c.before = c.steps()
		}
	}
	if c.f.i < c.at && !c.reached { // a choice before the node: named for its title
		var opts []string
		if c.sim != nil {
			opts = c.sim.ChoiceOptions(kind)
		}
		v := c.f.Choose(kind, n)
		pc := pathChoice{kind: kind, value: v}
		if v < len(opts) {
			pc.name = opts[v]
		}
		c.path = append(c.path, pc)
		return v
	}
	return c.f.Choose(kind, n)
}

// pathWords are a node's choices in words: "after 4 choices: client ran
// 4 times", with the faults made ("rpc.counter.request: duplicate"); its
// schedule id (0:1,1:1,2:1,3:1@4) meant nothing to a newcomer.
func pathWords(path []pathChoice) string {
	if len(path) == 0 {
		return "at the start"
	}
	ran, order := map[string]int{}, []string(nil)
	var faults []string
	for _, c := range path {
		switch {
		case c.kind == "sched.task" && c.name != "":
			if ran[c.name] == 0 {
				order = append(order, c.name)
			}
			ran[c.name]++
		case qenv.IsFault(c.kind) && c.value != 0:
			o := c.name
			if o == "" {
				o = fmt.Sprintf("option %d", c.value)
			}
			faults = append(faults, strings.TrimPrefix(c.kind, "fault.")+": "+o)
		}
	}
	var parts []string
	for _, t := range order {
		times := "once"
		if n := ran[t]; n > 1 {
			times = fmt.Sprintf("%d times", n)
		}
		parts = append(parts, t+" ran "+times)
	}
	parts = append(parts, faults...)
	words := fmt.Sprintf("after %d choices", len(path))
	if len(path) == 1 {
		words = "after 1 choice"
	}
	if len(parts) > 0 {
		words += ": " + strings.Join(parts, ", ")
	}
	return words
}

// foldable reports whether choice i, of n options, is no decision: one
// option, or nominal where the base run's (on its path) is nominal too.
func (c *navChooser) foldable(i, n int) bool {
	if n <= 1 || !c.onPath {
		return true
	}
	return i >= len(c.base) || c.base[i].Value == 0
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
// run alone GOTOGETHER_RUN names) as the base, then the node
// GOTOGETHER_NAV names from it, written where GOTOGETHER_NAV_OUT says,
// or (GOTOGETHER_NAV=serve) a session's nodes.
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
	b := e.navBase(r0)
	if os.Getenv("GOTOGETHER_NAV") == "serve" {
		e.serve(b, base)
		return
	}
	req := navproto.Request{Node: os.Getenv("GOTOGETHER_NAV"), Fold: os.Getenv("GOTOGETHER_NAV_FOLD") == "1", Save: os.Getenv("GOTOGETHER_NAV_SAVE") == "1"}
	for p := range strings.SplitSeq(os.Getenv("GOTOGETHER_NAV_FOLLOW"), ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		o, err := strconv.Atoi(p)
		if err != nil || o < 0 {
			t.Fatalf("gotogether: GOTOGETHER_NAV_FOLLOW %q: want options, comma-separated (0 nominal)", p)
		}
		req.Follow = append(req.Follow, o)
	}
	node, err := e.navNode(b, req)
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

// A navBase is what every node of a navigation starts from: the base run,
// its choices (but entropy) and their schedule, whether it drew entropy,
// and the checker that judges its nodes.
type navBase struct {
	r0      *Run
	ds      []deviation
	cs      []qenv.Choice
	entropy bool
	cc      *validate.Checker
	inst    string
}

func (e *engine) navBase(r0 *Run) *navBase {
	b := &navBase{r0: r0}
	b.ds, b.cs, b.entropy = baseSchedule(r0)
	var constants map[string]string
	b.cc, b.inst, constants = e.checker()
	setValidationInstance(e.x.profile, b.cc, constants)
	return b
}

// serve is a navigation session (GOTOGETHER_NAV=serve): requests from
// GOTOGETHER_NAV_IN (else stdin), one navproto.Request a line, each
// answered by a navproto.Response line on GOTOGETHER_NAV_OUT (else
// stdout), after a first line naming the session, until the input ends.
// The base run is made a second time first (an exploration's first seed
// is): a run that does not replay fails t, and the session says so, since
// none of its nodes would be what it shows.
func (e *engine) serve(b *navBase, base planned) {
	t := e.t
	t.Helper()
	in, out := io.Reader(os.Stdin), io.Writer(os.Stdout)
	if p := os.Getenv("GOTOGETHER_NAV_IN"); p != "" {
		f, err := os.Open(p)
		if err != nil {
			t.Fatalf("gotogether: GOTOGETHER_NAV_IN: %v", err)
		}
		defer f.Close()
		in = f
	}
	if p := os.Getenv("GOTOGETHER_NAV_OUT"); p != "" {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			t.Fatalf("gotogether: GOTOGETHER_NAV_OUT: %v", err)
		}
		defer f.Close()
		out = f
	}
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	send := func(r navproto.Response) {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("gotogether: GOTOGETHER_NAV=serve: %v", err)
		}
	}
	hello := &navproto.Session{Protocol: navproto.Protocol, Run: e.x.ref() + ":" + b.r0.ID(), Choices: len(b.cs)}
	again := base.chooser
	if base.again != nil {
		again = func() qenv.Chooser { return base.again(b.r0) }
	}
	if r := e.one(again()); r == nil {
		t.FailNow()
	} else if d := sameRun(b.r0, r); d != "" || errText(b.r0.Err) != errText(r.Err) {
		if d == "" {
			d = "its end: " + errText(b.r0.Err) + ", then " + errText(r.Err)
		}
		msg := fmt.Sprintf("%s ran twice made two runs, first different at %s: the system is not deterministic, so no node replays", e.x.nameOf(b.r0), d)
		t.Errorf("gotogether: %s", msg)
		hello.Notes = append(hello.Notes, msg)
	}
	if b.entropy {
		hello.Notes = append(hello.Notes, entropyNote)
	}
	send(navproto.Response{Session: hello})
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req navproto.Request
		if err := json.Unmarshal(line, &req); err != nil {
			send(navproto.Response{Error: fmt.Sprintf("a request is one JSON object a line (navproto.Request): %v", err)})
			continue
		}
		node, err := e.navNode(b, req)
		if err != nil {
			send(navproto.Response{Error: err.Error()})
			continue
		}
		send(navproto.Response{Node: node})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("gotogether: GOTOGETHER_NAV=serve: reading requests: %v", err)
	}
}

const entropyNote = "the base run drew entropy (qenv.EntropyOf), which a node's schedule does not replay: a node may diverge from it"

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

// spec is the node req names from the base: its node (start, fail, @k,
// an id), then its Follow and Take.
func (e *engine) spec(b *navBase, req navproto.Request) (nodeSpec, error) {
	var spec nodeSpec
	switch v := req.Node; {
	case v == "" || v == "start":
		spec = nodeSpec{nil, 0}
	case v == "fail":
		k, err := e.failingChoice(b)
		if err != nil {
			return spec, err
		}
		spec = nodeSpec{fixed(b.ds, k), k}
	case strings.HasPrefix(v, "@"):
		k, err := strconv.Atoi(v[1:])
		if err != nil || k < 0 {
			return spec, fmt.Errorf("node %q: want start, fail, @<k> or <schedule>@<k>", v)
		}
		spec = nodeSpec{fixed(b.ds, k), k}
	default:
		sched, at, ok := strings.Cut(v, "@")
		k, err := strconv.Atoi(at)
		if !ok || err != nil || k < 0 {
			return spec, fmt.Errorf("node %q: want start, fail, @<k> or <schedule>@<k> (a node's id)", v)
		}
		d, err := parseSchedule(sched)
		if err != nil {
			return spec, err
		}
		spec = nodeSpec{fixed(d, k), k}
	}
	follow := req.Follow
	if req.Take != nil {
		follow = append(slices.Clone(follow), *req.Take)
	}
	for _, o := range follow {
		if o < 0 {
			return spec, fmt.Errorf("follow %v: options are 0 (nominal) and up", follow)
		}
		spec = spec.child(o)
	}
	return spec, nil
}

// navNode is the node req names from the base: its replay, stopped (for
// what the node shows) at its choice, judged whole.
func (e *engine) navNode(b *navBase, req navproto.Request) (*navproto.Node, error) {
	t, x := e.t, e.x
	spec, err := e.spec(b, req)
	if err != nil {
		return nil, err
	}
	onPath := func(s nodeSpec) bool { return slices.Equal(s.ds, fixed(b.ds, s.at)) }
	nc := &navChooser{f: &forcing{ds: spec.ds}, at: spec.at, fold: req.Fold, base: b.cs, onPath: onPath(spec)}
	r := e.one(nc)
	if r == nil {
		t.FailNow()
	}
	spec.at = nc.at // past the choices it folded
	r.origin = origin{kind: "schedule", value: schedule(spec.ds)}
	node := &navproto.Node{Protocol: navproto.Protocol, Run: x.ref() + ":" + b.r0.ID(), ID: spec.id(), Schedule: schedule(spec.ds), At: spec.at, Folded: nc.folded, Branches: []navproto.Branch{}, Steps: []string{},
		Path: pathWords(nc.path)}
	if x.profile != nil {
		p := x.profile.Clone()
		p.Mode, p.Chooser, p.Parallel = "navigation", "navigation", 1
		p.Seeds, p.Rates, p.Exhaust = nil, nil, nil
		p.Replay = node.Run + " node=" + node.ID
		node.Profile = &p
	}
	if len(r.simulations) > 0 {
		execution := (runprofile.Execution{Run: node.Run, Simulations: r.simulations}).Clone()
		node.Execution = &execution
	}
	if b.entropy {
		node.Notes = append(node.Notes, entropyNote)
	}
	if r.Err != nil {
		node.Notes = append(node.Notes, "the node's run did not run to its end: "+r.Err.Error())
	}
	// a node of the base's own choices replays them: the system is
	// deterministic, or the node says where it is not
	if onPath(spec) {
		for i := 0; i < spec.at && i < len(b.cs); i++ {
			if i >= len(r.Choices) || r.Choices[i].Kind != b.cs[i].Kind || r.Choices[i].N != b.cs[i].N {
				node.Notes = append(node.Notes, fmt.Sprintf("the node's replay diverged from the base run at choice %d: the run is not deterministic, or draws entropy", i))
				break
			}
		}
	}
	if nc.reached {
		taken := 0
		if spec.at < len(b.cs) && onPath(spec) {
			taken = b.cs[spec.at].Value
		}
		node.Choice = &navproto.Choice{Kind: nc.kind, N: nc.n, Options: nc.options, Taken: taken}
		for o := range nc.n {
			br := navproto.Branch{Option: o, Child: spec.child(o).id()}
			if o < len(nc.options) {
				br.Name = nc.options[o]
			}
			node.Branches = append(node.Branches, br)
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
	node.Model = actorStates(b.cc, nc.before)
	judge(t, b.cc, b.inst, e.c.searchOptions(), []*Run{r}, func(*Run) string { return "the node's run " + node.ID })
	for _, actor := range r.Report.Actors {
		for _, ab := range actor.Absent {
			if ab.Diagnostic != nil {
				node.Diagnostics = append(node.Diagnostics, *ab.Diagnostic)
			}
		}
	}
	if r.Report.Composition != nil {
		r.Report.Composition.Run = node.Run
	}
	node.Receipt = r.Report.Receipt()
	if r.Report.Composition != nil && !r.Report.OK() {
		primaryPass := true
		for _, a := range r.Report.Actors {
			primaryPass = primaryPass && a.OK()
		}
		if primaryPass {
			node.Notes = append(node.Notes, "the primary-model steps conform; inspect the whole-run composition checks. There is no rejected primary-model step; the failing node opens the last simulation choice")
		}
	}
	switch {
	case len(r.Steps) == 0:
		// no step to fail at: what its doubles saw is the lead (a client
		// refused before its server listened), not a node that reads pass
		node.Receipt.Status = receipt.NotChecked
		node.Receipt.Reason = "no step recorded"
		node.Notes = append(node.Notes, r.noSteps())

	}
	if req.Save {
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

// failingChoice is the last choice the base run made before its failing step was
// recorded (index among cs, its choices but entropy): the decision
// nearest the failure. A run that passes has none.
func (e *engine) failingChoice(b *navBase) (int, error) {
	r0, cs := b.r0, b.cs
	r := *r0
	judge(e.t, b.cc, b.inst, e.c.searchOptions(), []*Run{&r}, func(*Run) string { return "the run" })
	if r.Report.OK() {
		return 0, fmt.Errorf("%s passes: there is no failing step to open (GOTOGETHER_NAV=start opens its first choice)", e.x.nameOf(r0))
	}
	pos := len(r0.Steps) // a run with no step: its last choice, where the node says what its doubles saw
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
func actorStates(cc *validate.Checker, steps []qtrace.Step) []navproto.ActorState {
	traces, _ := cc.Binding.Reconstruct(steps)
	var out []navproto.ActorState
	for _, tr := range traces {
		as := navproto.ActorState{Actor: tr.Actor}
		n, err := cc.Nav(validate.NavStart{Steps: steps, Actor: tr.Actor, At: -1})
		if err == nil {
			var nd *validate.Node
			if nd, err = n.Node(); err == nil {
				as.Diagnostics = nd.Diagnostics
				for _, v := range nd.State {
					as.State = append(as.State, navproto.NamedValue{Name: v.Name, Value: v.Value})
				}
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
	env := ""
	if m := Mutant(); m != "" { // the navigator builds the mutant's binary with its overlay
		env = MutantEnv + "=" + m + " "
		if p := os.Getenv(overlay.PackageEnv); p != "" { // of a harness's other package
			env += overlay.PackageEnv + "=" + p + " "
		}
	}
	return fmt.Sprintf("%sgotogether nav -run '%s:%s' -at fail (in the test's package directory; -json for agents)", env, x.ref(), r.ID())
}

// Unwrap implements qenv.Wrapper: the forcing chooser's error is the
// run's (chooserErr).
func (c *navChooser) Unwrap() qenv.Chooser { return c.f }
