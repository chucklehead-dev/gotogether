package binding

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Call is one step of the replay.
type Call struct {
	Quint  string // e.g. writeTable({ payload: 1, ... }, Ok)
	Expect string // post-state check, or ""
	// ExpectTemplate is the binding's expect that rendered Expect, whose
	// {{.obs.X}} the step's observations filled in
	ExpectTemplate string
	// ExpectArgs are the values of ExpectTemplate's fields, rendered as
	// Expect renders them: the arguments of the expect's definition in an
	// EvalModule (nil when the template is not text and fields)
	ExpectArgs []string
	Step       *qtrace.Step      // the observed step; nil for inferred steps
	Inferred   string            // why the step was inserted ("restart"), if inferred
	Action     string            // the model action
	Args       map[string]string // parameter -> the Quint expression passed
	Note       string            // description, for calls that come from an ITF state
}

// Plan is the binding applied to one actor's trace: the instance's
// constants and the calls, in model terms. It needs no Quint tooling.
type Plan struct {
	Actor         string
	Processes     []string
	ObservedSteps int
	Constants     map[string]string // constant -> Quint expression
	Calls         []Call
	// Start is the call the replay starts from instead of init: the
	// trace's first step, when it is the binding's snapshot action's
	// (Binding.Snapshot), the state it recorded. Nil: init.
	Start *Call
	// Issues are what planning found: a snapshot step not first in its
	// trace, skipped.
	Issues []qtrace.Issue
}

// ConstantList renders the constants as "NAME = expr", sorted.
func (p *Plan) ConstantList() []string {
	var out []string
	for _, c := range sortedKeys(p.Constants) {
		out = append(out, fmt.Sprintf("%s = %s", c, p.Constants[c]))
	}
	return out
}

// Plan applies the binding to a trace. With a model, parameters are taken
// in the model's order and checked; with m == nil (emitting a model-level
// ITF in-process, without Quint) they are keyed by name only.
func (b *Binding) Plan(m *Model, t *qtrace.Trace) (*Plan, error) { return b.Planner(m, t).Plan(t) }

// A Planner plans the orders of one history's steps (a search's
// candidates) in the history's instance: its identifiers interned in its
// order and its constants rendered once, for every candidate (they were
// computed again for each, a search's cost quadratic in the history).
// Its Plan of t, an order of some of the history's steps (a search's
// candidate), interns in the history's order and takes the constants
// from all its steps, so every candidate of one history is checked in
// one instance with one naming (a prefix's verdict, and its state, are
// then its extensions' too). Plan may be called from several goroutines.
type Planner struct {
	b      *Binding
	m      *Model
	whole  *qtrace.Trace
	tables map[ikey]map[string]int64 // whole's interning: read-only once made
	consts map[string]string
	err    error // the constants' rendering's
	mu     sync.Mutex
	rd     *renderer
}

type ikey struct{ arg, scope string }

// Planner is a planner of whole's orders.
func (b *Binding) Planner(m *Model, whole *qtrace.Trace) *Planner {
	p := &Planner{b: b, m: m, whole: whole, tables: map[ikey]map[string]int64{}, consts: map[string]string{}}
	all := make([]map[string]any, len(whole.Steps)) // whole's, for the constants
	for i := range whole.Steps {
		all[i] = p.intern(&whole.Steps[i], p.tables)
	}
	funcs := template.FuncMap{
		"set": func(names ...string) string {
			seen := map[string]bool{}
			var vs []string
			for _, a := range all {
				for _, arg := range names {
					if v, ok := a[arg]; ok && !seen[quintLit(v)] {
						seen[quintLit(v)] = true
						vs = append(vs, quintLit(v))
					}
				}
			}
			sort.Strings(vs)
			return "Set(" + strings.Join(vs, ", ") + ")"
		},
		"toSet":  func(v any) (string, error) { return jsonQuint(v, "Set") },
		"toList": func(v any) (string, error) { return jsonQuint(v, "List") },
		"toMap":  func(v any) (string, error) { return jsonQuint(v, "Map") },
		"max": func(arg string) int64 {
			var mx int64
			for _, a := range all {
				if v, ok := a[arg].(int64); ok && v > mx {
					mx = v
				}
			}
			return mx
		},
		"count": func(action string) int {
			n := 0
			for _, s := range whole.Steps {
				if s.Action == action {
					n++
				}
			}
			return n
		},
		"procs": func() int { return len(whole.Processes) },
		"add":   func(a, b any) int64 { return toI(a) + toI(b) },
		"atLeast": func(lo, v any) int64 {
			if toI(v) < toI(lo) {
				return toI(lo)
			}
			return toI(v)
		},
	}
	p.rd = newRenderer(funcs)
	for _, c := range sortedKeys(b.Constants) {
		v, err := p.rd.render(b.Constants[c], nil)
		if err != nil {
			p.err = fmt.Errorf("constant %s: %w", c, err)
			break
		}
		p.consts[c] = v
	}
	return p
}

// intern is s's arguments with opaque identifiers interned in tables,
// new ones numbered after those there.
// missing reports whether interning s would add an identifier to the
// history's tables.
func (p *Planner) missing(s *qtrace.Step) bool {
	for k, v := range s.Args {
		scope, ok := p.b.Intern[k]
		if !ok {
			continue
		}
		key := ikey{k, ""}
		if scope == "process" {
			key.scope = s.Process
		}
		if _, ok := p.tables[key][fmt.Sprint(v)]; !ok {
			return true
		}
	}
	return false
}

func (p *Planner) intern(s *qtrace.Step, tables map[ikey]map[string]int64) map[string]any {
	a := map[string]any{}
	for k, v := range s.Args {
		scope, ok := p.b.Intern[k]
		if !ok {
			a[k] = v
			continue
		}
		key := ikey{k, ""}
		if scope == "process" {
			key.scope = s.Process
		}
		tb := tables[key]
		if tb == nil {
			tb = map[string]int64{}
			tables[key] = tb
		}
		id := fmt.Sprint(v)
		if _, ok := tb[id]; !ok {
			tb[id] = int64(len(tb) + 1)
		}
		a[k] = tb[id]
	}
	return a
}

// Plan is t's plan, t an order of some of the history's steps.
func (p *Planner) Plan(t *qtrace.Trace) (*Plan, error) {
	if p.err != nil {
		return nil, p.err
	}
	b, m := p.b, p.m
	procIdx := map[string]int{}
	for i, pr := range t.Processes {
		procIdx[pr] = i + 1
	}
	// a candidate's steps interned in the history's tables, an identifier
	// they lack (a pending call's) numbered after them, in a copy
	var tables map[ikey]map[string]int64
	args := make([]map[string]any, len(t.Steps))
	for i := range t.Steps {
		if tables == nil && p.missing(&t.Steps[i]) {
			tables = map[ikey]map[string]int64{}
			for k, tb := range p.tables {
				tables[k] = maps.Clone(tb)
			}
		}
		if tables != nil {
			args[i] = p.intern(&t.Steps[i], tables)
		} else {
			args[i] = p.intern(&t.Steps[i], p.tables) // read-only here: every identifier is there
		}
	}
	plan := &Plan{Actor: t.Actor, Processes: t.Processes, ObservedSteps: len(t.Steps), Constants: maps.Clone(p.consts)}
	p.mu.Lock() // the renderer's templates, added as they are first used
	defer p.mu.Unlock()
	rd := p.rd

	// 3. Calls.
	prevProc := ""
	for i := range t.Steps {
		s := &t.Steps[i]
		switch {
		case s.Ambiguous:
			// a pending call's effect, placed by a search: in its caller's
			// process, which did not run there; it infers no restart
		case prevProc != "" && s.Process != prevProc && b.Restart != "":
			plan.Calls = append(plan.Calls, Call{Quint: b.Restart, Action: b.Restart, Args: map[string]string{},
				Inferred: fmt.Sprintf("restart: process %s -> %s", short(prevProc), short(s.Process))})
		case prevProc != "" && s.Process != prevProc:
			return nil, fmt.Errorf("step %d (%s): process changed from %s to %s and the binding has no restart action", i, s.Action, prevProc, s.Process)
		}
		if !s.Ambiguous {
			prevProc = s.Process
		}
		snapshot := b.Snapshot != "" && s.Action == b.Snapshot && !s.Ambiguous
		if snapshot && i > 0 {
			// the state it recorded may be checked against the replay's one
			// day; for now a snapshot is where a trace starts, nothing more
			plan.Issues = append(plan.Issues, qtrace.Issue{Severity: "info", Kind: "snapshot", Msg: fmt.Sprintf(
				"step %d (%s, seq %d%s) is a snapshot not first in its trace: skipped (a trace starts from its first step's snapshot, a log's head)", i+1, s.Action, s.Seq, where(s))})
			continue
		}
		ab := b.Actions[s.Action]
		var params []Param
		if m != nil {
			ps, ok := m.Actions[s.Action]
			if !ok {
				return nil, fmt.Errorf("step %d%s: %s: record the step under the model's action name", i+1, where(s), m.notAnAction(s.Action))
			}
			params = ps
		} else {
			for _, p := range sortedKeys(ab.Args) {
				params = append(params, Param{Name: p})
			}
		}
		outcome, err := b.outcome(ab, s)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i, err)
		}
		if outcome == "skip" {
			continue
		}
		data := stepData(s, args[i], procIdx[s.Process], outcome)
		c := Call{Step: s, Action: s.Action, Args: map[string]string{}}
		var vals []string
		for _, p := range params {
			tpl := ab.Template(p)
			v, err := rd.render(tpl, data)
			if err != nil {
				if f, ok := missingField(err); ok {
					return nil, fmt.Errorf("step %d (%s%s): parameter %s of %s reads the step's field %q, which the step does not have (%s). "+
						"Record %q with the step, or map the parameter to a field it has in the binding:\n  actions:\n    %s:\n      args: { %s: '{{.FIELD}}' }",
						i+1, s.Action, where(s), p.Name, sig(m, s.Action), f, hasFields(s), f, s.Action, p.Name)
				}
				return nil, fmt.Errorf("step %d (%s%s): parameter %s (template %s, signature %s): %w", i+1, s.Action, where(s), p.Name, tpl, sig(m, s.Action), err)
			}
			c.Args[p.Name] = v
			vals = append(vals, v)
		}
		c.Quint = s.Action
		if len(vals) > 0 {
			c.Quint += "(" + strings.Join(vals, ", ") + ")"
		}
		if ab.Expect != "" && !s.Ambiguous { // an assumed effect observed nothing
			c.ExpectTemplate = ab.Expect
			if parts, holes, err := splitCached(ab.Expect); err == nil {
				c.ExpectArgs = []string{} // a template of no fields has none, but has them
				for i, h := range holes {
					v, err := rd.render(h, data)
					if err != nil || !atomic(v, parts[i+1]) {
						c.ExpectArgs = nil
						break
					}
					c.ExpectArgs = append(c.ExpectArgs, v)
				}
			}
			if c.Expect, err = rd.render(ab.Expect, data); err != nil {
				if f, ok := missingField(err); ok {
					return nil, fmt.Errorf("step %d (%s%s): the binding's expect for %s reads the step's field %q, which the step does not have (%s): "+
						"record it with the step (observations as \"obs.NAME\")", i+1, s.Action, where(s), s.Action, f, hasFields(s))
				}
				return nil, fmt.Errorf("step %d (%s%s): expect: %w", i+1, s.Action, where(s), err)
			}
		}
		if snapshot {
			plan.Start = &c
			continue
		}
		plan.Calls = append(plan.Calls, c)
	}
	return plan, nil
}

// SnapshotFirst reports whether tr starts with a step of the binding's
// snapshot action: the state its replay starts from (Plan.Start), which
// every order a search tries keeps first.
func (b *Binding) SnapshotFirst(tr *qtrace.Trace) bool {
	return b.Snapshot != "" && len(tr.Steps) > 0 && tr.Steps[0].Action == b.Snapshot && !tr.Steps[0].Ambiguous
}

// StartIssues are tr's issues as its start says them: the lost prefix of
// the recorder whose snapshot step the trace starts with is what that
// step recorded, judged from there (info, no error). Another recorder's
// lost prefix stays an error: its lost steps may have come after the
// snapshot.
func (b *Binding) StartIssues(tr *qtrace.Trace) []qtrace.Issue {
	issues := slices.Clone(tr.Issues)
	if !b.SnapshotFirst(tr) {
		return issues
	}
	s := &tr.Steps[0]
	for _, l := range tr.Lost {
		if !l.Prefix || l.Recorder != s.Recorder || l.After+1 != s.Seq && !(l.After == 0 && s.Seq == 0) {
			continue
		}
		if i := slices.Index(issues, l.Issue()); i >= 0 {
			issues[i] = qtrace.Issue{Severity: "info", Kind: "lost-prefix", Msg: fmt.Sprintf("judged from the state recorded by %s at seq %d (%s)", s.Action, s.Seq, l)}
		}
	}
	return issues
}

// Skips reports whether the binding drops the step: its outcome maps to
// "skip" for its action, so it becomes no model call.
func (b *Binding) Skips(s *qtrace.Step) bool {
	o, err := b.outcome(b.Actions[s.Action], s)
	return err == nil && o == "skip"
}

func (b *Binding) outcome(a Action, s *qtrace.Step) (string, error) {
	o := s.Outcome
	if o == "" {
		o = "ok"
	}
	if v, ok := a.Outcomes[o]; ok {
		if v == "call" {
			return "", nil // replay as if it succeeded
		}
		return v, nil
	}
	if o != "ok" && !a.usesOutcome() {
		// A failed step of an action whose call ignores the outcome would be
		// replayed as a success. Make the binding say what it means.
		return "", fmt.Errorf("%s failed (outcome %q: %s) but its templates do not use .outcome; map it in the action's outcomes (e.g. %s: skip)", s.Action, o, s.Error, o)
	}
	if v, ok := b.Outcomes[o]; ok {
		return v, nil
	}
	if o == "ok" {
		return "", nil // only an error if a template uses .outcome
	}
	return "", fmt.Errorf("%s: outcome %q has no mapping in the binding (error: %s)", s.Action, o, s.Error)
}

// String describes the call: its step's seq, process and action, or why
// it was inferred.
func (c Call) String() string {
	if c.Step == nil {
		if c.Inferred == "" {
			return c.Note
		}
		return "inferred " + c.Inferred
	}
	s := c.Step
	return fmt.Sprintf("seq %d %s %s", s.Seq, short(s.Process), s.String())
}

func short(s string) string {
	if len(s) > 24 {
		return s[:24] + "…"
	}
	return s
}

func toI(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	}
	return 0
}

// jsonQuint renders a collection recorded as JSON text (qobs records slices
// as JSON) as a Quint Set(...) or List(...); nested arrays become tuples.
func jsonQuint(v any, kind string) (string, error) {
	text := fmt.Sprint(v)
	if q, ok := v.(qlit); ok {
		u, err := strconv.Unquote(string(q))
		if err != nil {
			return "", fmt.Errorf("to%s: %s is not a recorded JSON array", kind, q)
		}
		text = u
	}
	if kind == "Map" {
		var m map[string]any
		if err := json.Unmarshal([]byte(text), &m); err != nil {
			return "", fmt.Errorf("toMap: %q is not a JSON object: %v", text, err)
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = strconv.Quote(k) + " -> " + jsonElem(m[k])
		}
		return "Map(" + strings.Join(parts, ", ") + ")", nil
	}
	var xs []any
	if err := json.Unmarshal([]byte(text), &xs); err != nil {
		return "", fmt.Errorf("to%s: %q is not a JSON array: %v", kind, text, err)
	}
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = jsonElem(x)
	}
	if kind == "List" {
		return "[" + strings.Join(parts, ", ") + "]", nil
	}
	return "Set(" + strings.Join(parts, ", ") + ")", nil
}

// jsonElem renders a decoded JSON value as Quint: arrays become tuples.
func jsonElem(x any) string {
	switch y := x.(type) {
	case []any:
		parts := make([]string, len(y))
		for i, e := range y {
			parts[i] = jsonElem(e)
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case float64:
		return strconv.FormatInt(int64(y), 10)
	case string:
		return strconv.Quote(y)
	case bool:
		return strconv.FormatBool(y)
	}
	return strconv.Quote(fmt.Sprint(x))
}

// sig is an action's signature, if the model is known.
func sig(m *Model, action string) string {
	if m == nil {
		return action
	}
	return m.Signature(action)
}

var reMissing = regexp.MustCompile(`at <\.([\w.]+)>: map has no entry for key`)

// missingField is the step field a template read and the step lacks
// ("obs.balance", "amount"), from text/template's missingkey error.
func missingField(err error) (string, bool) {
	m := reMissing.FindStringSubmatch(err.Error())
	if m == nil {
		return "", false
	}
	return strings.TrimPrefix(m[1], "args."), true
}

// hasFields lists the fields a step has, for a message about one it lacks.
func hasFields(s *qtrace.Step) string {
	var fs []string
	for k := range s.Args {
		fs = append(fs, k)
	}
	for k := range s.Obs {
		fs = append(fs, "obs."+k)
	}
	if len(fs) == 0 {
		return "it has no fields"
	}
	sort.Strings(fs)
	return "it has: " + strings.Join(fs, ", ")
}

// where is a step's source for messages (", line 3"), if known.
func where(s *qtrace.Step) string {
	if s.Source == "" {
		return ""
	}
	return ", " + s.Source
}
