// Package binding is the declarative, per-project half of gotogether: a YAML
// file that says how observed steps become calls of a Quint model's actions.
// It holds no Go code, so applying gotogether to a new project means writing
// annotations and one binding file (see the README's "applying to a new
// project").
package binding

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"
	"time"
	"unicode/utf8"

	"github.com/chucklehead-dev/gotogether/internal/memo"
	"github.com/chucklehead-dev/gotogether/itf"
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
	// Actors are the actors whose traces the binding checks: glob patterns
	// (* any run of characters, / and . included, since an actor's name is
	// no path: `*.clients` matches `t/x.clients`; ? one character; [a-z]
	// and [!a-z] a class; \c the character c), a pattern written "!p"
	// excluding the actors p matches. Empty: every actor. A trace of
	// another actor is another binding's, left out of every check and of
	// StepsToITF, and named with its step count in the report's issues: so
	// one run that records two sides (a system's commits under the
	// tenants' names, its clients' calls under "<tenant>.clients") is
	// checked by two bindings of one model, `actors: ["!*.clients"]` and
	// `actors: ["*.clients"]`. Every step still reconstructs, so a gap or a
	// settle is found over the whole run (Reconstruct).
	Actors []string `yaml:"actors"`
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
	// Complete says every run checked against the binding is a finished
	// one (a whole journey, settled, every call answered), not a prefix:
	// `gotogether validate` then fails one that is not, as with
	// -require-finish. It needs an eventual val, the journey's end: without
	// one a settled prefix would pass. Tests state it as a claim instead
	// (qtest.Finishes), which this does not change.
	Complete bool `yaml:"complete"`
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
	// Path is the binding file, absolute (Load): what a run's manifest
	// hashes (validate.Manifest).
	Path string `yaml:"-"`
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
	// Possible are what the model must be able to do (Possible): some
	// trace reaches each. A model too strict to reach a scenario a
	// conforming run would show fails `gotogether simulate` here, before
	// any code is checked against it.
	Possible []Possible `yaml:"possible"`
	// SearchStates bounds the Go evaluator's search for the possibles
	// (default 2000 states; its depth is Steps): a search the bound cut
	// says "not found within the bound", never "impossible".
	SearchStates int `yaml:"searchStates"`
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
	// Terminal is a Quint predicate over the state saying where the model
	// means to stop: `gotogether simulate -graph` reports a state the step
	// cannot leave as a deadlock unless it holds there (a protocol that
	// has finished, a log at its bound). A state the step can leave only
	// by a stutter (a read) is never a deadlock, so a model that can
	// always read needs none: the report counts such states as quiet.
	Terminal string `yaml:"terminal"`
	// GraphStates and GraphDepth bound `gotogether simulate -graph`'s
	// state graph for this model (default 100000 states, no depth bound):
	// a model whose state grows without end (grist's log) needs the
	// depth bound to explore every state near its start.
	GraphStates int `yaml:"graphStates"`
	GraphDepth  int `yaml:"graphDepth"`
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
	bd.Dir, bd.Path = filepath.Dir(abs), abs
	if bd.Spec == "" || bd.Module == "" {
		return nil, fmt.Errorf("%s: spec and module are required", path)
	}
	if err := bd.checkActors(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
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

// checkActors fails on a pattern of Actors that is no glob.
func (b *Binding) checkActors() error {
	for _, p := range b.Actors {
		pat := strings.TrimPrefix(p, "!")
		if _, err := actorGlob(pat); err != nil || pat == "" {
			why := "empty"
			if err != nil {
				why = err.Error()
			}
			return fmt.Errorf("actors: the pattern %q is not a glob (%s; * any characters, / included, ? one, [a-z] a class; \"!p\" excludes p's actors)", p, why)
		}
	}
	return nil
}

// ChecksActor reports whether the binding checks actor's trace (Actors):
// it matches an including pattern (or there is none) and no excluding
// one.
func (b *Binding) ChecksActor(actor string) bool {
	in, includes := false, false
	for _, p := range b.Actors {
		if pat, ok := strings.CutPrefix(p, "!"); ok {
			if re, err := actorGlob(pat); err == nil && re.MatchString(actor) {
				return false
			}
			continue
		}
		includes = true
		if re, err := actorGlob(p); err == nil && re.MatchString(actor) {
			in = true
		}
	}
	return in || !includes
}

// Reconstruct is qtrace.Reconstruct of steps, keeping the traces of the
// actors the binding checks (Actors): every step reconstructs, so a gap
// or a settle is found over the whole run, and another actor's trace is
// another binding's. Any left out are named, with their steps, in an
// info issue "actors"; a run none of whose actors the binding checks has
// a warning naming them (and, with no actor, no verdict).
func (b *Binding) Reconstruct(steps []qtrace.Step) ([]*qtrace.Trace, []qtrace.Issue) {
	traces, issues := qtrace.Reconstruct(steps)
	if len(b.Actors) == 0 {
		return traces, issues
	}
	kept := traces[:0:0]
	var others []string
	for _, t := range traces {
		if b.ChecksActor(t.Actor) {
			kept = append(kept, t)
			continue
		}
		name := t.Actor
		if name == "" {
			name = "default"
		}
		others = append(others, fmt.Sprintf("%s (%d steps, %d pending)", name, len(t.Steps), len(t.Pending)))
	}
	switch {
	case len(kept) == 0 && len(others) > 0:
		issues = append(issues, qtrace.Issue{Severity: "warn", Kind: "actors", Msg: fmt.Sprintf(
			"the binding checks the actors %s; the run's are all other bindings' (%s): fix the binding's actors: patterns, or record under the actors it names",
			strings.Join(b.Actors, ", "), strings.Join(others, ", "))})
	case len(others) > 0:
		issues = append(issues, qtrace.Issue{Severity: "info", Kind: "actors", Msg: fmt.Sprintf(
			"left out, another binding's (actors: %s): %s", strings.Join(b.Actors, ", "), strings.Join(others, ", "))})
	}
	return kept, issues
}

// actorGlob compiles an actors: pattern: * any run of characters, / and
// . included (an actor's name is no path: `*.clients` matches
// `t/x.clients`), ? one character, [a-z] and [!a-z] a class, \c the
// character c.
func actorGlob(pat string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pat); i++ {
		switch pat[i] {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '\\':
			if i+1 == len(pat) {
				return nil, fmt.Errorf("a \\ at the end")
			}
			i++
			b.WriteString(regexp.QuoteMeta(pat[i : i+1]))
		case '[':
			j := strings.IndexByte(pat[i+1:], ']')
			if j < 0 {
				return nil, fmt.Errorf("a [ without its ]")
			}
			class := pat[i+1 : i+1+j]
			if class == "" || class == "!" || class == "^" {
				return nil, fmt.Errorf("an empty class []")
			}
			b.WriteString("[")
			if class[0] == '!' || class[0] == '^' {
				b.WriteString("^")
				class = class[1:]
			}
			b.WriteString(strings.NewReplacer(`\`, `\\`, `[`, `\[`).Replace(class))
			b.WriteString("]")
			i += j + 1
		default:
			r, n := utf8.DecodeRuneInString(pat[i:])
			b.WriteString(regexp.QuoteMeta(string(r)))
			i += n - 1
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// SpecPath is the absolute path of the model.
func (b *Binding) SpecPath() string {
	if filepath.IsAbs(b.Spec) {
		return b.Spec
	}
	return filepath.Join(b.Dir, b.Spec)
}

// CheckSpec fails when the spec is not where the binding says, naming
// the binding file, the spec as it resolves from there, and, when the
// spec is found relative to the working directory instead, the path to
// write: quint's own error was a bare chdir failure. Load does not
// check, since a binding is read (a manifest's) whose model is gone.
func (b *Binding) CheckSpec() error {
	resolved := b.SpecPath()
	if _, err := os.Stat(resolved); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	fix := "fix spec: it is relative to the binding file's directory"
	if !filepath.IsAbs(b.Spec) {
		if abs, err := filepath.Abs(b.Spec); err == nil {
			if _, err := os.Stat(abs); err == nil {
				if rel, err := filepath.Rel(b.Dir, abs); err == nil {
					fix = fmt.Sprintf("it is relative to the binding file's directory, not the working directory: write spec: %s", filepath.ToSlash(rel))
				}
			}
		}
	}
	return fmt.Errorf("%s: spec %s resolves to %s, which does not exist (%s)", b.relPath(), b.Spec, resolved, fix)
}

// relPath is the binding's file relative to the working directory when
// it is below it, else absolute; "binding" for one not loaded from a
// file.
func (b *Binding) relPath() string {
	if b.Path == "" {
		return "binding"
	}
	if wd, err := os.Getwd(); err == nil {
		if r, err := filepath.Rel(wd, b.Path); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return b.Path
}

// Lint checks the binding against the model: every bound action exists and
// is given exactly its parameters, the restart action exists and takes none,
// invariants are vals, templates parse, and every constant is set. It returns
// all problems found.
func (b *Binding) Lint(m *Model) []string {
	var errs []string
	for _, name := range slices.Sorted(maps.Keys(b.Actions)) {
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
	if b.Complete && len(b.Eventually) == 0 {
		errs = append(errs, "complete with no eventual vals: a settled prefix of a run would pass as complete; state the journey's end as one (eventually: [ended], a ghost flag its last step sets)")
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
	for _, c := range slices.Sorted(maps.Keys(m.Consts)) {
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
	return strings.Join(slices.Sorted(maps.Keys(vars)), ", ")
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
	return slices.Sorted(maps.Keys(set))
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
	"toMap": func(any) (string, error) { return "", nil }, "toQuint": func(string, any) (string, error) { return "", nil },
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
	case json.Number: // an integer past int64, kept whole (qtrace.Step.Args)
		if _, ok := new(big.Int).SetString(string(x), 10); ok {
			return string(x)
		}
		f, _ := x.Float64()
		return strconv.FormatInt(int64(f), 10)
	case string:
		return quintStr(x)
	case qlit:
		return string(x)
	}
	return quintStr(fmt.Sprint(v))
}

// quintStr is s as a Quint string literal: its characters between double
// quotes, as they are, since Quint has no escapes ("a\tb" is a backslash
// and a t, a tab is itself). A string with a double quote has none; the
// renderer refuses one a template prints (qstr), and toQuint and toList
// refuse one inside a value.
func quintStr(s string) string { return `"` + s + `"` }

// qstr is a recorded string as templates see it: printed as its Quint
// literal (quintStr), its own text for toQuint, toList and the others
// to decode. One with a double quote prints marked (unquotable), and
// the renderer refuses the result, naming the field.
type qstr string

func (s qstr) String() string {
	if strings.Contains(string(s), `"`) {
		return unquotable + string(s) + unquotable
	}
	return quintStr(string(s))
}

// unquotable brackets a printed string that no Quint literal can hold:
// a renderer finding it fails, naming the field.
const unquotable = "\x00gotogether:unquotable\x00"

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
		d[k] = fieldValue(v)
		as[k] = d[k]
	}
	// set after the arguments: these names are the step's, and an argument
	// of the same name is reached through .args
	d["args"] = as
	d["step"] = map[string]any{"action": s.Action, "seq": s.Seq}
	obs := map[string]any{}
	for k, v := range s.Obs {
		obs[k] = fieldValue(v)
	}
	d["obs"] = obs
	d["process"] = qlit(strconv.Itoa(process))
	d["outcome"] = qlit(outcome)
	return d
}

// fieldValue is a step field as templates see it: Quint text, but a
// float kept whole (printed truncated, as before) for toQuint to refuse.
func fieldValue(v any) any {
	switch x := v.(type) {
	case float64:
		return newQfloat(x)
	case string:
		return qstr(x)
	}
	return qlit(quintLit(v))
}

func render(tpl string, data any, funcs template.FuncMap) (string, error) {
	return newRenderer(funcs).render(tpl, data)
}

// funcNames are the binding's template functions, by name: a template
// parses against them (which checks only that they exist) once, and runs
// with each plan's own.
var funcNames = []string{"set", "toSet", "toList", "toMap", "toQuint", "max", "count", "procs", "add", "atLeast"} // read-only

// parsed caches each template text's parse tree, process-wide (a
// template parses the same in every plan): parsing it for every
// candidate a search planned was a third of a long linearization's time.
var parsed memo.Map[string, parsedTree] // process-wide: template text -> its tree (or error)

type parsedTree struct {
	tree *parse.Tree
	err  error
}

func treeOf(tpl string) (*parse.Tree, error) {
	p := parsed.Once(tpl, func() parsedTree {
		stubs := template.FuncMap{}
		for _, f := range funcNames {
			stubs[f] = func(...any) any { return nil }
		}
		t, err := template.New("").Funcs(stubs).Parse(tpl)
		if err != nil {
			return parsedTree{err: err}
		}
		return parsedTree{tree: t.Tree}
	})
	return p.tree, p.err
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
	out := buf.String()
	if i := strings.Index(out, unquotable); i >= 0 {
		rest := out[i+len(unquotable):]
		s, _, _ := strings.Cut(rest, unquotable)
		return "", fmt.Errorf("%s: %w (in %s)", fieldNamed(data, s), &itf.UnquotableError{At: "the template's output", S: s}, tpl)
	}
	return out, nil
}

// fieldNamed names the step field of data (stepData's) that holds s: "the
// step's field x", "obs.x", or "a step field" if none does.
func fieldNamed(data any, s string) string {
	d, ok := data.(map[string]any)
	if !ok {
		return "a step field"
	}
	var names []string
	for k, v := range d {
		if v == qstr(s) && k != "args" {
			names = append(names, k)
		}
	}
	if obs, ok := d["obs"].(map[string]any); ok {
		for k, v := range obs {
			if v == qstr(s) {
				names = append(names, "obs."+k)
			}
		}
	}
	if len(names) == 0 {
		return "a step field"
	}
	sort.Strings(names)
	return "the step's field " + strings.Join(names, ", ")
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
