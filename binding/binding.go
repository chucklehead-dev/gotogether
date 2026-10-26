// Package binding is the declarative, per-project half of gotogether: a YAML
// file that says how observed steps become calls of a Quint model's actions.
// It holds no Go code, so applying gotogether to a new project means writing
// annotations and one binding file (see the README's "applying to a new
// project").
package binding

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"text/template/parse"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"gopkg.in/yaml.v3"
)

// Binding is the parsed binding file.
type Binding struct {
	// Spec is the .qnt file (relative to the binding file) and Module the
	// parameterised module in it to instantiate.
	Spec   string `yaml:"spec"`
	Module string `yaml:"module"`
	// Constants of the instance, as Quint expressions. They are templates
	// over the whole trace (see Stats), so bounds can follow what was seen.
	Constants map[string]string `yaml:"constants"`
	// Intern maps an argument name to a scope ("actor" or "process"). Its
	// observed values (opaque ids like "g20260924T101500") are renamed
	// 1, 2, 3, ... in order of first appearance: the abstraction function
	// from implementation identifiers to the model's small integers.
	Intern map[string]string `yaml:"intern"`
	// Outcomes maps a recorded quint.outcome to a Quint expression, available
	// to templates as .outcome. Actions may override it.
	Outcomes map[string]string `yaml:"outcomes"`
	// Restart is the model action inserted where an actor's process changes:
	// the crash a dead process cannot report. Empty: a process change is an error.
	Restart string `yaml:"restart"`
	// Defs is Quint text added to the generated module (helpers for templates).
	Defs string `yaml:"defs"`
	// Antecedents name, per invariant, the condition under which it says
	// something (Quint over the model's state): where it never holds, the
	// invariant holds vacuously. An invariant `A implies B` has A without
	// saying so (Model.Antecedents); give one for any other form, such as
	// `xs.forall(x => P(x) implies Q(x))`, whose antecedent is
	// `xs.exists(x => P(x))`.
	Antecedents map[string]string `yaml:"antecedents"`
	// Actions maps a model action to the templates that build its call.
	Actions map[string]Action `yaml:"actions"`
	// Invariants are vals of the model checked in every observed state.
	Invariants []string `yaml:"invariants"`
	// Eventually are vals of the model that must hold when the run ends
	// (no work pending: `outbox.size() == 0`): liveness obligations,
	// checked in the last observed state only, and only of a trace that
	// recorded its system settled (a settle record: qenv's Sim.Settle, or
	// qobs.Settled), where pending work is a failure to finish rather than
	// a run cut short. Every check honours it: explorations, Record,
	// Validate, qhegel and the CLI.
	Eventually []string `yaml:"eventually"`
	// Within are bounded-response obligations: each time a val (After)
	// becomes true, another (Holds) must hold within Bound of virtual (or
	// recorded) time, in the replay's states, timed by their steps. A
	// trace that ends before the bound with the obligation open is no
	// verdict, unless it recorded its system settled (nothing changes
	// after). Every check of a run honours it.
	Within []Within `yaml:"within"`
	// Hidden are model actions that happen and are never recorded (a
	// gossip round, a handoff): between recorded steps any sequence of
	// them, at most HiddenDepth long (default 4), may have run. Each takes
	// no parameters (give the model a wrapper that picks them with
	// nondet). A trace is then judged by the Go evaluator, which searches
	// the states they reach: quint cannot replay a step whose choices are
	// unknown.
	Hidden      []string `yaml:"hidden"`
	HiddenDepth int      `yaml:"hiddenDepth"`
	// Snapshot is a model action whose parameters are the whole state
	// (it assigns every variable from them: restore(balance, ops)): a
	// step of it first in its trace is the state the trace starts from,
	// instead of init. A log that lost its prefix (rotated, truncated, a
	// recorder started mid-run) is judged from the snapshot recorded at
	// its head; a snapshot step anywhere else is skipped, with an issue.
	Snapshot string `yaml:"snapshot"`
	// Resume is a nullary model action whose nondet picks give the states
	// a trace that lost its prefix may start in (bounded by the model's
	// constants and invariants: action resumeAny = nondet b = 0.to(LIMIT)
	// .oneOf() all { balance' = b, ops' = 0 }). A trace whose first
	// recorder lost its prefix, with no snapshot step at its head, is
	// judged from those states by the Go evaluator's search (quint
	// confirms its verdict): it conforms if some admitted state explains
	// the rest. A snapshot step, where there is one, is used instead.
	Resume string `yaml:"resume"`
	// Simulate says what the model alone does with its invariants
	// (`gotogether simulate`): which it guarantees, which it allows to fail.
	Simulate *Simulation `yaml:"simulate"`

	Dir string `yaml:"-"` // directory of the binding file
}

// A Within is a bounded-response obligation (Binding.Within): after After
// becomes true, Holds within Bound ("3s", "500ms").
type Within struct {
	After string        `yaml:"after"`
	Holds string        `yaml:"holds"`
	Bound time.Duration `yaml:"-"`
	// BoundText is the bound as written
	BoundText string `yaml:"bound"`
}

// Name is how a result names the obligation among a run's invariants:
// "within 3s after healed: hasLeader". qtest.Violates takes it so.
func (w Within) Name() string {
	return fmt.Sprintf("within %s after %s: %s", w.BoundText, w.After, w.Holds)
}

// Simulation is how the model runs on its own (`quint run`), and what it
// must show there. An invariant a binding checks is either a property of
// the design, which the model guarantees, or a requirement on the code,
// which the model allows to fail (a provider can be charged twice; the
// code must not do it). Each of the binding's invariants is in one list.
type Simulation struct {
	// Constants override the binding's for the simulation: those that are
	// templates over a run (`{{ set "node" }}`) need a value here.
	Constants map[string]string `yaml:"constants"`
	// Holds are the invariants the model guarantees: no trace violates them.
	Holds []string `yaml:"holds"`
	// Allows are the invariants the model allows to fail: some trace must
	// violate each, or checking the code against it proves nothing.
	Allows []string `yaml:"allows"`
	// Steps and Samples bound the run (default 30 and 1000).
	Steps   int `yaml:"steps"`
	Samples int `yaml:"samples"`
	// Skip, if set, says why the model is not run on its own: a replay-only
	// model, whose actions take observed values, has no step to simulate.
	Skip string `yaml:"skip"`
	// VerifySteps caps `gotogether simulate -verify` for this model:
	// Apalache's cost grows steeply with depth, and differently by model
	// (0: no cap).
	VerifySteps int `yaml:"verifySteps"`
	// VerifySkip, if set, says why -verify skips the model: one Apalache
	// cannot encode, or one too slow at any useful depth. Its sampling
	// stands.
	VerifySkip string `yaml:"verifySkip"`
}

// Action says how to call one model action from an observed step.
type Action struct {
	// Args maps model parameters to Quint expression templates over the
	// step (see StepData). A parameter not given is the step field of the
	// same name, converted by the parameter's type (Param.DefaultTemplate):
	// `{{.p}}`, or `{{ toSet .p }}` for a set, toList for a list, toMap for
	// a map. An action whose parameters all default needs no entry.
	Args map[string]string `yaml:"args"`
	// Expect is a Quint boolean over the post-state that must hold after the
	// step: the observed state projection (quint.obs.*) checked against the
	// model's state.
	Expect string `yaml:"expect"`
	// Outcomes overrides the binding's outcome map for this action. "skip"
	// drops steps with that outcome (the model has no such step); "call"
	// replays them like successful ones. A non-ok outcome of an action whose
	// templates ignore .outcome must be mapped here, or generation fails.
	Outcomes map[string]string `yaml:"outcomes"`
}

// Load reads a binding file.
func Load(path string) (*Binding, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bd Binding
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&bd); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	abs, _ := filepath.Abs(path)
	bd.Dir = filepath.Dir(abs)
	if bd.Spec == "" || bd.Module == "" {
		return nil, fmt.Errorf("%s: spec and module are required", path)
	}
	for i := range bd.Within {
		w := &bd.Within[i]
		d, err := time.ParseDuration(w.BoundText)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: bound %q: %v (a Go duration: 3s, 500ms)", path, w.Name(), w.BoundText, err)
		}
		w.Bound = d
	}
	return &bd, nil
}

// SpecPath is the absolute path of the model.
func (b *Binding) SpecPath() string {
	if filepath.IsAbs(b.Spec) {
		return b.Spec
	}
	return filepath.Join(b.Dir, b.Spec)
}

// Lint checks the binding against the model: every bound action exists and
// is given exactly its parameters, the restart action exists and takes none,
// invariants are vals, templates parse, and every constant is set. It returns
// all problems found.
func (b *Binding) Lint(m *Model) []string {
	var errs []string
	for _, name := range sortedKeys(b.Actions) {
		a := b.Actions[name]
		ps, ok := m.Actions[name]
		if !ok {
			errs = append(errs, "action "+m.notAnAction(name))
			continue
		}
		want := map[string]bool{}
		for _, p := range ps {
			want[p.Name] = true
		}
		for arg, tpl := range a.Args {
			if !want[arg] {
				errs = append(errs, fmt.Sprintf("action %s: %q is not a parameter; signature %s", name, arg, m.Signature(name)))
			}
			if _, err := parseTpl(tpl); err != nil {
				errs = append(errs, fmt.Sprintf("action %s arg %s: %v", name, arg, err))
			}
		}
		if a.Expect != "" {
			if _, err := parseTpl(a.Expect); err != nil {
				errs = append(errs, fmt.Sprintf("action %s expect: %v", name, err))
			}
		}
	}
	if b.Restart != "" {
		if ps, ok := m.Actions[b.Restart]; !ok {
			errs = append(errs, "restart: "+m.notAnAction(b.Restart))
		} else if len(ps) != 0 {
			errs = append(errs, fmt.Sprintf("restart: %s takes parameters; a restart action must take none", m.Signature(b.Restart)))
		}
	}
	for _, inv := range b.Invariants {
		if !m.Vals[inv] {
			errs = append(errs, fmt.Sprintf("invariant %s: not a val of %s", inv, m.Module))
		}
	}
	for _, ev := range b.Eventually {
		if !m.Vals[ev] {
			errs = append(errs, fmt.Sprintf("eventually %s: not a val of %s", ev, m.Module))
		}
	}
	seenHidden := map[string]bool{}
	for _, h := range b.Hidden {
		ps, ok := m.Actions[h]
		switch {
		case seenHidden[h]:
			errs = append(errs, fmt.Sprintf("hidden %s: listed twice", h))
		case h == "init" || h == "step":
			errs = append(errs, fmt.Sprintf("hidden %s: the model's %s is no action of the system: name the actions that run unrecorded", h, h))
		case h == b.Restart:
			errs = append(errs, fmt.Sprintf("hidden %s is the restart action, which the binding infers where the process changes: it is not hidden", h))
		case !ok:
			errs = append(errs, fmt.Sprintf("hidden %s: not an action of %s", h, m.Module))
		case len(ps) > 0:
			errs = append(errs, fmt.Sprintf("hidden %s takes parameters: a hidden action's choices are unknown; give the model one that picks them (action %sAny = nondet ... %s(...))", h, h, h))
		}
		if _, bound := b.Actions[h]; bound {
			errs = append(errs, fmt.Sprintf("hidden %s is bound too: an action is recorded (actions) or hidden, not both", h))
		}
		seenHidden[h] = true
	}
	if a := b.Snapshot; a != "" {
		ps, ok := m.Actions[a]
		switch {
		case a == "init" || a == "step":
			errs = append(errs, fmt.Sprintf("snapshot %s: the model's %s is no snapshot: name an action whose parameters are the whole state (action restore(b: int, n: int) = all { balance' = b, ops' = n })", a, a))
		case !ok:
			errs = append(errs, "snapshot: "+m.notAnAction(a))
		case a == b.Restart || seenHidden[a]:
			errs = append(errs, fmt.Sprintf("snapshot %s is the binding's restart or a hidden action: a snapshot is a recorded step of its own", a))
		case len(ps) == 0:
			errs = append(errs, fmt.Sprintf("snapshot %s takes no parameters: a snapshot's parameters are the state the trace starts from (action %s(b: int, ...) = all { balance' = b, ... })", a, a))
		default:
			if vars, known := m.Assigns(a); known {
				var left []string
				for _, v := range m.Vars {
					if !vars[v] {
						left = append(left, v)
					}
				}
				if len(left) > 0 {
					errs = append(errs, fmt.Sprintf("snapshot %s leaves %s unassigned: a snapshot assigns every state variable from its parameters (%s' = ...), the whole state the trace starts from",
						a, strings.Join(left, ", "), left[0]))
				}
			}
			if r := readVars(m, a); r != "" {
				errs = append(errs, fmt.Sprintf("snapshot %s reads %s: a trace starts in it from no state, so it assigns every variable from its parameters alone", a, r))
			}
		}
	}
	if a := b.Resume; a != "" {
		ps, ok := m.Actions[a]
		switch {
		case a == "init" || a == "step":
			errs = append(errs, fmt.Sprintf("resume %s: the model's %s is no resume: name a nullary action that picks the states a log may start in (action resumeAny = nondet b = 0.to(LIMIT).oneOf() all { balance' = b, ops' = 0 })", a, a))
		case !ok:
			errs = append(errs, "resume: "+m.notAnAction(a))
		case a == b.Restart || seenHidden[a] || a == b.Snapshot:
			errs = append(errs, fmt.Sprintf("resume %s is the binding's restart, a hidden action or its snapshot: a resume is no step, only the states a trace may start in", a))
		case len(ps) > 0:
			errs = append(errs, fmt.Sprintf("resume %s takes parameters: a resume picks the states itself (nondet ... oneOf()), and takes none", m.Signature(a)))
		default:
			if vars, known := m.Assigns(a); known {
				var left []string
				for _, v := range m.Vars {
					if !vars[v] {
						left = append(left, v)
					}
				}
				if len(left) > 0 {
					errs = append(errs, fmt.Sprintf("resume %s leaves %s unassigned: a resume assigns every state variable (%s' = ...), the whole state a trace may start in",
						a, strings.Join(left, ", "), left[0]))
				}
			}
			if r := readVars(m, a); r != "" {
				errs = append(errs, fmt.Sprintf("resume %s reads %s: a trace starts in it from no state, so it picks every variable from constants and values alone (nondet b = 0.to(LIMIT).oneOf())", a, r))
			}
		}
		if _, bound := b.Actions[a]; bound {
			errs = append(errs, fmt.Sprintf("resume %s is bound too: it is no recorded step (record a snapshot for that: snapshot)", a))
		}
	}
	if b.HiddenDepth < 0 {
		errs = append(errs, "hiddenDepth: negative")
	}
	if b.HiddenDepth != 0 && len(b.Hidden) == 0 {
		errs = append(errs, "hiddenDepth with no hidden actions: list them (hidden: [gossip])")
	}
	for _, w := range b.Within {
		for _, v := range []string{w.After, w.Holds} {
			if !m.Vals[v] {
				errs = append(errs, fmt.Sprintf("%s: %q is not a val of %s (within takes vals: after, holds)", w.Name(), v, m.Module))
			}
		}
		if w.Bound <= 0 {
			errs = append(errs, fmt.Sprintf("%s: no bound: give a positive duration (bound: 3s)", w.Name()))
		}
	}
	for _, c := range sortedNames(m.Consts) {
		if v, ok := b.Constants[c]; !ok || strings.TrimSpace(v) == "" {
			// an empty one (scaffold's '') once linted and failed in quint's parser
			errs = append(errs, fmt.Sprintf("constant %s: no value (type %s): give it a Quint expression (%s: '3')", c, m.Consts[c], c))
		}
	}
	for c := range b.Constants {
		if _, ok := m.Consts[c]; !ok {
			errs = append(errs, fmt.Sprintf("constant %s: not a const of %s", c, m.Module))
		}
	}
	for arg, scope := range b.Intern {
		if scope != "actor" && scope != "process" {
			errs = append(errs, fmt.Sprintf("intern %s: scope must be actor or process, not %q", arg, scope))
		}
	}
	return errs
}

// readVars names the state variables action reads ("" for none, or a
// model that does not say).
func readVars(m *Model, action string) string {
	vars, known := m.Reads(action)
	if !known || len(vars) == 0 {
		return ""
	}
	return strings.Join(sortedKeys(vars), ", ")
}

// Fields returns the step fields (.name) a template refers to: what the
// instrumentation must record for this action. ps are the action's
// parameters, whose defaults count too (nil: only the given templates).
func (a Action) Fields(ps ...Param) []string {
	set := map[string]bool{}
	for _, t := range a.Args {
		collectFields(t, set)
	}
	for _, p := range ps {
		if _, ok := a.Args[p.Name]; !ok {
			collectFields(p.DefaultTemplate(), set)
		}
	}
	collectFields(a.Expect, set)
	// the step's own identity, not fields it records; seq and action may be
	// arguments (they win over the step's own), so they stay
	delete(set, "outcome")
	delete(set, "process")
	delete(set, "step")
	for f := range set {
		if a, ok := strings.CutPrefix(f, "args."); ok {
			delete(set, f)
			set[a] = true
		}
	}
	return sortedKeys(set)
}

// Template is the template of an action's parameter: the binding's, else
// the default.
func (a Action) Template(p Param) string {
	if t, ok := a.Args[p.Name]; ok {
		return t
	}
	return p.DefaultTemplate()
}

func (a Action) usesOutcome() bool {
	set := map[string]bool{}
	for _, t := range a.Args {
		collectFields(t, set)
	}
	collectFields(a.Expect, set)
	return set["outcome"]
}

// templateFuncs names the functions templates may call (Plan defines them;
// these stand-ins let a template parse for linting).
// Read-only.
var templateFuncs = template.FuncMap{
	"set": func(...string) string { return "" }, "max": func(string) int64 { return 0 }, "count": func(string) int { return 0 },
	"procs": func() int { return 0 }, "add": func(a, b any) int64 { return 0 }, "atLeast": func(lo, v any) int64 { return 0 },
	"toSet": func(any) (string, error) { return "", nil }, "toList": func(any) (string, error) { return "", nil },
	"toMap": func(any) (string, error) { return "", nil },
}

func parseTpl(s string) (*template.Template, error) {
	return template.New("").Option("missingkey=error").Funcs(templateFuncs).Parse(s)
}

func collectFields(s string, set map[string]bool) {
	t, err := parseTpl(s)
	if err != nil || t.Tree == nil {
		return
	}
	var walk func(n parse.Node)
	walk = func(n parse.Node) {
		switch x := n.(type) {
		case *parse.ListNode:
			if x != nil {
				for _, c := range x.Nodes {
					walk(c)
				}
			}
		case *parse.ActionNode:
			walk(x.Pipe)
		case *parse.PipeNode:
			if x != nil {
				for _, c := range x.Cmds {
					walk(c)
				}
			}
		case *parse.CommandNode:
			for _, a := range x.Args {
				walk(a)
			}
		case *parse.FieldNode:
			set[strings.Join(x.Ident, ".")] = true
		case *parse.IfNode:
			walk(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		}
	}
	walk(t.Tree.Root)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// quintLit renders an observed value as a Quint literal.
func quintLit(v any) string {
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case string:
		return strconv.Quote(x)
	case qlit:
		return string(x)
	}
	return strconv.Quote(fmt.Sprint(v))
}

// qlit is a value that is already Quint text.
type qlit string

func (q qlit) String() string { return string(q) }

// StepData is what an action template sees: the step's arguments by name
// (interned where configured, rendered as Quint literals), plus
//
//	.process  the actor's incarnation, 1-based
//	.outcome  the outcome, mapped through the outcome table
//	.obs.X    an observed projection
//	.action   .seq   unless the step has an argument of that name, which
//	                 wins: a parameter named seq is the argument seq. The
//	                 step's own are always .step.action and .step.seq.
//	.args.X   every argument, whatever its name: an argument named
//	          process, outcome, obs, step or args is reached only here
//	          (a parameter of that name defaults to it)
func stepData(s *qtrace.Step, args map[string]any, process int, outcome string) map[string]any {
	d := map[string]any{}
	d["action"] = s.Action
	d["seq"] = s.Seq
	as := map[string]any{}
	for k, v := range args {
		d[k] = qlit(quintLit(v))
		as[k] = d[k]
	}
	// set after the arguments: these names are the step's, and an argument
	// of the same name is reached through .args
	d["args"] = as
	d["step"] = map[string]any{"action": s.Action, "seq": s.Seq}
	obs := map[string]any{}
	for k, v := range s.Obs {
		obs[k] = qlit(quintLit(v))
	}
	d["obs"] = obs
	d["process"] = qlit(strconv.Itoa(process))
	d["outcome"] = qlit(outcome)
	return d
}

func render(tpl string, data any, funcs template.FuncMap) (string, error) {
	return newRenderer(funcs).render(tpl, data)
}

// funcNames are the binding's template functions, by name: a template
// parses against them (which checks only that they exist) once, and runs
// with each plan's own.
var funcNames = []string{"set", "toSet", "toList", "toMap", "max", "count", "procs", "add", "atLeast"} // read-only

// parsed caches each template text's parse tree, process-wide (a
// template parses the same in every plan): parsing it for every
// candidate a search planned was a third of a long linearization's time.
var parsed sync.Map // process-wide: template text -> *parse.Tree (or error)

func treeOf(tpl string) (*parse.Tree, error) {
	if v, ok := parsed.Load(tpl); ok {
		if err, bad := v.(error); bad {
			return nil, err
		}
		return v.(*parse.Tree), nil
	}
	stubs := template.FuncMap{}
	for _, f := range funcNames {
		stubs[f] = func(...any) any { return nil }
	}
	t, err := template.New("").Funcs(stubs).Parse(tpl)
	if err != nil {
		parsed.Store(tpl, err)
		return nil, err
	}
	parsed.Store(tpl, t.Tree)
	return t.Tree, nil
}

// A renderer renders templates with one plan's functions, bound once: a
// template set with them, each text added to it as a parsed tree.
type renderer struct {
	base  *template.Template
	named map[string]*template.Template
}

func newRenderer(funcs template.FuncMap) *renderer {
	return &renderer{base: template.New("").Option("missingkey=error").Funcs(funcs), named: map[string]*template.Template{}}
}

func (r *renderer) render(tpl string, data any) (string, error) {
	t := r.named[tpl]
	if t == nil {
		tree, err := treeOf(tpl)
		if err != nil {
			return "", err
		}
		if t, err = r.base.New(tpl).AddParseTree(tpl, tree); err != nil {
			return "", err
		}
		r.named[tpl] = t
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// UsedFields lists, for each action of the model, the step fields the
// binding reads: its templates' and its parameters' defaults. What a log
// needs to keep to be checked (qtrace.Filter.Keep).
func (b *Binding) UsedFields(m *Model) map[string][]string {
	out := map[string][]string{}
	for name, ps := range m.Actions {
		out[name] = b.Actions[name].Fields(ps...)
	}
	return out
}
