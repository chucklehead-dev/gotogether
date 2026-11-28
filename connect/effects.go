package connect

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/effectsdiff"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Effects in connect-driven runs (docs/DESIGN.md §115): a Binding given
// Effects compares the effects each step made (qobs.Effect, a qenv
// double's send, through the trace's context) with the model's
// <action>Effects def, as validate compares a recorded step's (§112),
// through the same mapping (binding.Effects) and its one normalization
// (binding.Effects.Check), reported in the same shape (EffectsMismatch).

// An EffectsMismatch is why a step failed when its effects are not the
// model's (StepError.Effects): the effects made and the def's, and where
// they first differ. It is validate's EffectsMismatch: one report for a
// recorded run and a model-driven one.
type EffectsMismatch = effectsdiff.Mismatch

// effectsOf is EffectsOf's word on one action's effects.
type effectsOf struct{ def, match string }

// Effects maps the effects the implementation records (qobs.Effect, a
// qenv double's send) onto the model's effect constructors, as a binding
// file's effects section does (binding.Effects: from, where, fields and
// same mean what they mean there), and compares every step's with its
// action's effects def: <action>Effects when the model has it (initEffects
// for the constructor), else EffectsOf's. A step of an action with no
// def that made an effect of a mapped kind fails: the model states none.
//
//	b := connect.Bind(newGateway).
//		Action("attempt", (*Gateway).Attempt, "c", "d").
//		Effects(binding.Effects{Kinds: map[string]binding.EffectKind{
//			"Send": {From: "upstream.request", Fields: map[string]string{
//				"call": "{{.call}}", "deployment": "{{.deployment}}", "model": "{{.model}}"}},
//		}}).
//		EffectsOf("attempt", "", binding.MatchRetried)
//
// The effects are recorded through the trace's context: the one a
// method's context.Context parameter receives, Step.Context in a
// handler, BindContext's constructor's. A step's effects are those
// recorded while it ran, in their order; one recorded between two steps
// (a goroutine still sending) fails the next step, or the trace after
// its last. An effect sent through no such context (context.Background()
// on the driver's goroutine or another) is not recorded: a missing send
// then fails, an extra one passes. The mapping is checked against the
// model before any trace (Run, Test): each def, its result's type, each
// constructor and its fields. Effects given twice panics.
func (b *Binding[T]) Effects(fx binding.Effects) *Binding[T] {
	if b.effects != nil {
		panic("connect.Bind: Effects given twice: give one mapping")
	}
	b.effects = &fx
	return b
}

// EffectsOf says how action's effects are compared (an action bound, or
// init): def is the model's def that states them ("" for the default,
// <action>Effects; "-" compares none), match the mode (binding.MatchExact,
// the default for ""; MatchRetried, collapsing consecutive equal tries
// whose Same fields agree; MatchSet). It needs Effects, and is checked
// with it before any trace. An action given twice panics.
func (b *Binding[T]) EffectsOf(action, def, match string) *Binding[T] {
	if _, dup := b.effectsOf[action]; dup {
		panic(fmt.Sprintf("connect.Bind: EffectsOf(%q) given twice: give each action one", action))
	}
	if b.effectsOf == nil {
		b.effectsOf = map[string]effectsOf{}
	}
	b.effectsOf[action] = effectsOf{def, match}
	return b
}

// effectsDefs are the defs a Binding with Effects may compare with,
// compiled for before the check: EffectsOf's, and each bound action's
// (and init's) default, which the model may lack.
func (b *Binding[T]) effectsDefs() []string {
	if b.effects == nil {
		return nil
	}
	var out []string
	for _, a := range append([]string{"init"}, b.order...) {
		if def := b.effectsOf[a].def; def != "-" {
			if def == "" {
				def = a + "Effects"
			}
			if !slices.Contains(out, def) {
				out = append(out, def)
			}
		}
	}
	return out
}

// effectsDef is the def action's effects compare with in m, and the
// mode: EffectsOf's, else <action>Effects if m has it; none, with
// optedOut, for "-".
func (b *Binding[T]) effectsDef(m *qeval.Model, action string) (def, match string, optedOut bool) {
	of := b.effectsOf[action]
	match = of.match
	if match == "" {
		match = binding.MatchExact
	}
	switch of.def {
	case "-":
		return "", "", true
	case "":
		if isValue(m, action+"Effects") {
			return action + "Effects", match, false
		}
		return "", binding.MatchExact, false
	}
	return of.def, match, false
}

// isValue reports whether name is a val or a def of m.
func isValue(m *qeval.Model, name string) bool {
	if m == nil {
		return false
	}
	d, ok := m.Decl(name)
	if !ok || d.Kind != "def" {
		return false
	}
	switch d.Qualifier {
	case "val", "def", "pureval", "puredef":
		return true
	}
	return false
}

// checkEffects is the effects' mismatches with m, before any trace: the
// mapping, each EffectsOf, each def (its parameters the action's, its
// result a list or set of a sum type), and each mapped constructor one
// of that type's, its fields exactly its record's.
func (b *Binding[T]) checkEffects(m *qeval.Model, c Config) (out []string) {
	bad := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	fx := b.effects
	if fx == nil {
		for _, a := range slices.Sorted(maps.Keys(b.effectsOf)) {
			bad("connect.Bind: EffectsOf(%q) without Effects compares nothing: give the Binding's Effects, the mapping of the recorded effects onto the model's constructors", a)
		}
		return out
	}
	if len(fx.Kinds) == 0 {
		bad("connect.Bind: Effects maps no kind: map each of the model's effect constructors to the effects recorded as it (Kinds: {\"Send\": {From: \"upstream.request\", Fields: ...}})")
		return out
	}
	for _, ctor := range slices.Sorted(maps.Keys(fx.Kinds)) {
		if fx.Kinds[ctor].From == "" {
			bad("connect.Bind: Effects' %s has no From: name the recorded effect kind it maps (From: \"upstream.request\")", ctor)
		}
	}
	for _, a := range slices.Sorted(maps.Keys(b.effectsOf)) {
		of := b.effectsOf[a]
		if _, ok := b.actions[a]; !ok && a != "init" {
			bad("connect.Bind: EffectsOf(%q): no action %s is bound (bound: %s): bind it with Action, Handle or Skip, or name init, the constructor", a, a, strings.Join(b.order, ", "))
		}
		switch of.match {
		case "", binding.MatchExact, binding.MatchRetried, binding.MatchSet:
		default:
			bad("connect.Bind: EffectsOf(%q): match %q is no mode: give binding.MatchExact, MatchRetried or MatchSet (\"\" is exact)", a, of.match)
		}
		if of.def == "-" && of.match != "" {
			bad("connect.Bind: EffectsOf(%q): match %q with no def (\"-\" compares none): drop the match, or name the def", a, of.match)
		}
	}
	type sumOf struct {
		def string
		t   *qeval.Type
	}
	var sums []sumOf
	resolved := 0
	for _, a := range append([]string{"init"}, b.order...) {
		of := b.effectsOf[a]
		def := of.def
		switch def {
		case "-":
			continue
		case "":
			if !isValue(m, a+"Effects") {
				continue
			}
			def = a + "Effects"
		default:
			if !isValue(m, def) {
				bad("connect.Bind: EffectsOf(%q): %s is no val or def of %s: name the def that states %s's effects (def %sEffects(...): List[Effect])", a, def, c.Spec, a, a)
				continue
			}
		}
		resolved++
		d, _ := m.Decl(def)
		var have []string
		if a != "init" {
			if ad, ok := m.Decl(a); ok {
				for _, p := range m.Params(ad) {
					have = append(have, p.Name)
				}
			}
		}
		for _, p := range m.Params(d) {
			if !slices.Contains(have, p.Name) {
				takes := "takes no parameters"
				if len(have) > 0 {
					takes = "takes " + strings.Join(have, ", ")
				}
				bad("connect.Bind: action %s: its effects def %s takes %s, which %s does not (it %s): a def's parameters are the action's, by name", a, def, p.Name, a, takes)
			}
		}
		t, ok := m.TypeOf(d)
		if !ok {
			continue // an IR without types: the traces judge it
		}
		if t.Kind == "oper" {
			t = t.Res
		}
		if t == nil || (t.Kind != "list" && t.Kind != "set") || t.Elem == nil || t.Elem.Kind != "sum" {
			bad("connect.Bind: action %s: its effects def %s is %s, no list or set of the model's effect type: give it a result List[Effect] over a sum type (type Effect = Send({ ... }) | ...)", a, def, t)
			continue
		}
		sums = append(sums, sumOf{def, t.Elem})
	}
	if resolved == 0 {
		bad("connect.Bind: Effects maps %s, but no action has an effects def to compare with (no <action>Effects or initEffects in %s, none named by EffectsOf): state one in the model (def attemptEffects(...): List[Effect]), or drop Effects",
			strings.Join(slices.Sorted(maps.Keys(fx.Kinds)), ", "), c.Spec)
		return out
	}
	if len(sums) == 0 {
		return out
	}
	for _, ctor := range slices.Sorted(maps.Keys(fx.Kinds)) {
		var variant *qeval.Field
		var ctors []string
		for _, s := range sums {
			for i, v := range s.t.Fields {
				if !slices.Contains(ctors, v.Name) {
					ctors = append(ctors, v.Name)
				}
				if v.Name == ctor && variant == nil {
					variant = &s.t.Fields[i]
				}
			}
		}
		if variant == nil {
			slices.Sort(ctors)
			bad("connect.Bind: Effects maps %s, which is no constructor of the effects defs' type (%s's: %s): map the model's constructors", ctor, sums[0].def, strings.Join(ctors, ", "))
			continue
		}
		var want []string
		switch p := variant.Type; {
		case p == nil:
			continue
		case p.Nullary():
		case p.Kind == "rec":
			for _, f := range p.Fields {
				want = append(want, f.Name)
			}
		default:
			bad("connect.Bind: Effects maps %s, whose payload is %s, no record: an effect constructor carries a record (%s({ field: ... })) or nothing", ctor, p, ctor)
			continue
		}
		got := slices.Sorted(maps.Keys(fx.Kinds[ctor].Fields))
		slices.Sort(want)
		if slices.Equal(got, want) {
			continue
		}
		var missing, extra []string
		for _, f := range want {
			if !slices.Contains(got, f) {
				missing = append(missing, f)
			}
		}
		for _, f := range got {
			if !slices.Contains(want, f) {
				extra = append(extra, f)
			}
		}
		var why []string
		if len(missing) > 0 {
			why = append(why, "missing "+strings.Join(missing, ", "))
		}
		if len(extra) > 0 {
			why = append(why, "not the model's "+strings.Join(extra, ", "))
		}
		wantText := "none"
		if len(want) > 0 {
			wantText = strings.Join(want, ", ")
		}
		bad("connect.Bind: Effects' %s maps the fields %s, but the model's %s has %s (%s): map exactly its record's fields", ctor, choose(len(got) == 0, "none", strings.Join(got, ", ")), ctor, wantText, strings.Join(why, "; "))
	}
	return out
}

// An effectWindow is one trace's effects: the recorder the trace's
// context carries (a sink of its own, so that none made is none), and
// what it recorded since the last step's window closed.
type effectWindow struct {
	rec  *qobs.Recorder
	ctx  context.Context
	mu   sync.Mutex
	made []qtrace.Step
	// last is the step whose window closed last (nil before init): an
	// effect recorded after it is no step's
	last *Step
}

func newEffectWindow(ctx context.Context) *effectWindow {
	w := &effectWindow{}
	w.rec = qobs.NewRecorder(w, qobs.ObserveEffects())
	w.ctx = qobs.WithRecorder(ctx, w.rec)
	return w
}

// Record keeps the effect records (qtrace.KeyEffect); the steps the code
// records itself (qobs.Record, Invoke) are not connect's to judge.
func (w *effectWindow) Record(_ context.Context, s *qtrace.Step) {
	if s.Effect == "" {
		return
	}
	w.mu.Lock()
	w.made = append(w.made, *s)
	w.mu.Unlock()
}

// take is what was recorded since the last take, in seq order, as
// Reconstruct attaches effects to a step.
func (w *effectWindow) take() []qtrace.Effect {
	w.mu.Lock()
	recs := w.made
	w.made = nil
	w.mu.Unlock()
	slices.SortStableFunc(recs, func(a, b qtrace.Step) int { return cmp.Compare(a.Seq, b.Seq) })
	out := make([]qtrace.Effect, 0, len(recs))
	for _, e := range recs {
		out = append(out, qtrace.Effect{Kind: e.Effect, Fields: e.Args, Recorder: e.Recorder, Seq: e.Seq, Time: e.Time, Call: e.Call})
	}
	return out
}

// stray is the effects recorded since the last step's window closed,
// before s's opens, as s's failure: no step made them. At the trace's
// end (s nil), those after its last step.
func (w *effectWindow) stray(s *Step) error {
	made := w.take()
	if len(made) == 0 {
		return nil
	}
	var es []string
	for _, e := range made {
		es = append(es, e.String())
	}
	after := "init"
	if w.last != nil {
		after = fmt.Sprintf("step %d (%s)", w.last.Index, w.last.summary())
	}
	when := "and before this step began"
	if s == nil {
		when = "and before the trace ended (its last step)"
	}
	return &effectsFailure{err: fmt.Errorf("%s made after %s returned %s: no step's. Make the driver wait for the work its step starts (a goroutine, a request in flight) before it returns, as a settle does; or, if the design sends later, give that send an action of its own",
		andList(es), after, when)}
}

// effectsFailure is a step's effects failing: a mismatch with the
// model's def, or an error judging them (a def the evaluator cannot
// compute, an effect a template cannot render, effects no step made),
// with the step's own error after it. Replay reports it as a StepError
// of kind "effects".
type effectsFailure struct {
	mismatch *EffectsMismatch
	err      error
	step     error // the step's own error (or panic), after the effects'
}

func (e *effectsFailure) Error() string {
	var b strings.Builder
	if e.mismatch != nil {
		b.WriteString(e.mismatch.Brief())
	} else if e.err != nil {
		b.WriteString(e.err.Error())
	}
	if e.step != nil {
		fmt.Fprintf(&b, "; and the step failed: %v", e.step)
	}
	return b.String()
}

// cause is the StepError's Err: the error judging the effects, else
// the step's own error, after the mismatch.
func (e *effectsFailure) cause() error {
	if e.err != nil && e.step != nil {
		return errors.Join(e.err, e.step)
	}
	if e.err != nil {
		return e.err
	}
	return e.step
}

// judgeEffects compares the effects s made (its window's) with its
// action's def in the trace's post-state: nil when they are the model's.
func (d *boundDriver[T]) judgeEffects(s *Step) *effectsFailure {
	made := d.fx.take()
	d.fx.last = s
	fail := func(format string, a ...any) *effectsFailure {
		return &effectsFailure{err: fmt.Errorf("connect.Bind: %s's effects: %s", s.Action, fmt.Sprintf(format, a...))}
	}
	if s.eval == nil {
		return fail("this step has no model to evaluate its effects def in: replay through connect.Run or connect.Test (or the Binding's Run and Test), which compile the model")
	}
	m, err := s.eval.model(s.Action + "Effects")
	if err != nil {
		return fail("%v", err)
	}
	def, match, optedOut := d.b.effectsDef(m, s.Action)
	if optedOut {
		return nil
	}
	fx := d.b.effects
	if def == "" {
		ec, err := fx.Check("", s.Action+"Effects", "", made)
		if err != nil {
			return fail("%v", err)
		}
		if !ec.Undeclared() {
			return nil
		}
		x := &EffectsMismatch{Call: ec.Call, Match: ec.Match, Undeclared: true, Expected: []string{}, Mapping: "the Binding's Effects",
			Drop: fmt.Sprintf("EffectsOf(%q, \"-\", \"\")", s.Action)}
		x.Made, x.MadeAt = printed(ec.Made), madeAt(ec.From)
		x.Index, x.Field, x.Got, x.Want = binding.FirstDifference(x.Made, nil)
		return &effectsFailure{mismatch: x}
	}
	if m, err = s.eval.model(def); err != nil {
		return fail("%v", err)
	}
	dd, ok := m.Decl(def)
	if !ok {
		return fail("%s is no definition of %s: name the def that states %s's effects", def, s.eval.c.Spec, s.Action)
	}
	var args []any
	var texts []string
	for _, p := range m.Params(dd) {
		v, ok := s.Picks[p.Name]
		if !ok {
			return fail("%s's parameter %s is no pick of the step (have %s): a def's parameters are the action's, by name", def, p.Name, s.pickNames())
		}
		args, texts = append(args, v), append(texts, v.String())
	}
	call := def
	if len(texts) > 0 {
		call += "(" + strings.Join(texts, ", ") + ")"
	}
	ec, err := fx.Check(def, call, match, made)
	if err != nil {
		return fail("%v", err)
	}
	v, _, err := s.evalValue(def, args...)
	if err != nil {
		return &effectsFailure{err: err} // a def outside the fragment, or undefined here: never a pass
	}
	var expected []string
	switch l := v.(type) {
	case qeval.List:
		for _, el := range l {
			expected = append(expected, qeval.String(el))
		}
	case *qeval.Set:
		for _, el := range l.Elems {
			expected = append(expected, qeval.String(el))
		}
	default:
		return fail("%s is %s, no list of effects: give it a result List[Effect]", call, qeval.String(v))
	}
	got := printed(ec.Made)
	if match == binding.MatchSet {
		got, expected = asSet(got), asSet(expected)
	}
	x := &EffectsMismatch{Call: call, Match: match, Made: got, MadeAt: madeAt(ec.From), Expected: expected, Tries: ec.Tries, Mapping: "the Binding's Effects"}
	if expected == nil {
		x.Expected = []string{}
	}
	x.Index, x.Field, x.Got, x.Want = binding.FirstDifference(x.Made, x.Expected)
	if x.Index == 0 {
		return nil
	}
	return &effectsFailure{mismatch: x}
}

// printed are effects as Quint text, as the evaluator prints values, so
// that they compare with a def's value text for text; one it cannot
// read stays as rendered (and differs).
func printed(made []string) []string {
	out := make([]string, 0, len(made))
	for _, s := range made {
		if v, err := itf.ParseQuint(s); err == nil {
			if q, err := qeval.FromITF(v); err == nil {
				s = qeval.String(q)
			}
		}
		out = append(out, s)
	}
	return out
}

// asSet is vs deduplicated and sorted: a set's elements, compared as
// match set compares them.
func asSet(vs []string) []string {
	out := slices.Clone(vs)
	slices.Sort(out)
	return slices.Compact(out)
}

func madeAt(from []qtrace.Effect) []string {
	var out []string
	for _, e := range from {
		out = append(out, fmt.Sprintf("%s at seq %d", e.Kind, e.Seq))
	}
	return out
}
