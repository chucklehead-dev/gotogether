package binding

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// I/O specifications: the effects a step must make (docs/DESIGN.md, "I/O
// specifications"). The model states them as a def per action over the
// post-state (`def payEffects(order: str): List[Effect]`, over a sum type
// it declares); the binding maps recorded effects (qobs.Effect, qenv's
// doubles at the send) onto the model's constructors; the step's expect
// gains one conjunct, `payEffects("o1") == [Capture({ ... })]`, judged as
// every expect is, by quint and by the Go evaluator.

// Effects is the binding's effects section: how recorded effects become
// values of the model's effect type.
type Effects struct {
	// Kinds maps each of the model's effect constructors to the effects
	// recorded as it.
	Kinds map[string]EffectKind `yaml:"kinds"`
}

// An EffectKind says which recorded effects are one constructor's, and
// how its fields are built from theirs.
type EffectKind struct {
	// From is the recorded effect's kind (qtrace.Effect.Kind:
	// "rpc.provider.request", "queue.payments.publish").
	From string `yaml:"from"`
	// Where, if set, keeps the effects of From whose recorded fields have
	// these values (as text): one kind of request carries several
	// operations ({ Op: capture }).
	Where map[string]string `yaml:"where"`
	// Fields map the constructor's record fields to Quint templates over
	// the effect's recorded fields ('{{.Amount}}'), rendered as an
	// action's arguments are. None: a constructor of no payload.
	Fields map[string]string `yaml:"fields"`
	// Same are recorded fields every try of one effect carries alike (an
	// idempotency key): under match retried, consecutive equal effects
	// collapse into one only if these agree too, so a retry under a fresh
	// key is two effects where the model makes one.
	Same []string `yaml:"same"`
}

// The match modes of an action's effects (Action.Match).
const (
	MatchExact   = "exact"   // as recorded, in order
	MatchRetried = "retried" // consecutive equal tries (their Same fields too) collapsed into one
	MatchSet     = "set"     // deduplicated and unordered
)

// An EffectCheck is a call's comparison of the effects its step made
// with its action's effects def (Call.Effects): what the expect's added
// conjunct says, for a report.
type EffectCheck struct {
	// Def is the model's def and Call its application to the step's
	// arguments ("payEffects(\"o1\")"), Match the mode.
	Def, Call, Match string
	// Made are the step's effects of mapped kinds as the model's values,
	// normalized by Match; From are the effects each came from (the first
	// try, for a collapsed retry); Literal is the list (or set) they make.
	Made    []string
	From    []qtrace.Effect
	Literal string
	// Tries say, for match retried, where two consecutive tries of one
	// effect disagreed in a Same field (two keys): the retry the model
	// counts twice.
	Tries []string
	// NotCompared are the step's effects of kinds the binding does not
	// map: not judged, listed.
	NotCompared []qtrace.Effect
}

// effectsDef is the model's def that states action's effects and the
// mode they are matched in, or "" when it has none: the action's own
// effects key ("-" for none), else, when the binding maps kinds,
// <action>Effects if the model has it.
func (b *Binding) effectsDef(m *Model, action string) (def, match string) {
	a := b.Actions[action]
	match = a.Match
	if match == "" {
		match = MatchExact
	}
	switch {
	case a.Effects == "-":
		return "", ""
	case a.Effects != "":
		return a.Effects, match
	case b.Effects != nil && len(b.Effects.Kinds) > 0 && m != nil:
		if _, ok := m.Defs[action+"Effects"]; ok {
			return action + "Effects", match
		}
	}
	return "", ""
}

// comparesEffects reports whether the binding maps any effect kind:
// then every step's effects of those kinds are judged, an action's
// against its def or, with none, against none (EffectCheck.Undeclared).
func (b *Binding) comparesEffects() bool { return b.Effects != nil && len(b.Effects.Kinds) > 0 }

// lintEffects checks the effects section and every action's effects def
// against the model.
func (b *Binding) lintEffects(m *Model, add func(code, symbol, fix, message string)) {
	if b.Effects != nil {
		for _, ctor := range slices.Sorted(maps.Keys(b.Effects.Kinds)) {
			k := b.Effects.Kinds[ctor]
			if k.From == "" {
				add("binding.effects.from", ctor, "Name the recorded effect kind the constructor maps.", fmt.Sprintf("effects.kinds.%s: no from: name the recorded effect kind it maps (from: rpc.provider.request)", ctor))
			}
			for _, f := range slices.Sorted(maps.Keys(k.Fields)) {
				if _, err := parseTpl(k.Fields[f]); err != nil {
					add("binding.template.invalid", ctor, "Correct the effect field's template.", fmt.Sprintf("effects.kinds.%s.fields.%s: %v", ctor, f, err))
				}
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(b.Actions)) {
		a := b.Actions[name]
		switch a.Match {
		case "", MatchExact, MatchRetried, MatchSet:
		default:
			add("binding.effects.match", name, "Use exact, retried or set.", fmt.Sprintf("action %s: match %q is no mode (exact, retried or set)", name, a.Match))
		}
		def, _ := b.effectsDef(m, name)
		if def == "" {
			if a.Match != "" && a.Effects != "-" {
				add("binding.effects.match", name, "Give the action an effects def, or drop match.", fmt.Sprintf("action %s: match with no effects def: name the def (effects: %sEffects)", name, name))
			}
			continue
		}
		if b.Effects == nil || len(b.Effects.Kinds) == 0 {
			add("binding.effects.kinds", name, "Map the recorded effects onto the model's constructors in effects.kinds.", fmt.Sprintf("action %s: effects %s, but the binding maps no effect kinds: add effects: { kinds: { Ctor: { from: <recorded kind>, fields: {...} } } }", name, def))
		}
		ps, ok := m.Defs[def]
		if !ok {
			add("binding.effects.def", name, "Name a def of the model with the action's parameters.", fmt.Sprintf("action %s: effects %s is not a def of %s with parameters: declare `def %s(...): List[Effect]` over the post-state", name, def, m.Module, def))
			continue
		}
		have := map[string]bool{}
		for _, p := range m.Actions[name] {
			have[p.Name] = true
		}
		for _, p := range ps {
			if !have[p] {
				add("binding.effects.def", name, "Give the effects def only parameters the action has.", fmt.Sprintf("action %s: its effects def %s takes %s, which %s does not: a def's parameters are the action's, by name", name, def, p, m.Signature(name)))
			}
		}
	}
}

// constructor is the constructor e is recorded as, or "" for an effect
// the mapping does not map.
func (fx *Effects) constructor(e *qtrace.Effect) string {
	if fx == nil {
		return ""
	}
	for _, ctor := range slices.Sorted(maps.Keys(fx.Kinds)) {
		k := fx.Kinds[ctor]
		if k.From != e.Kind {
			continue
		}
		ok := true
		for f, want := range k.Where {
			if v, has := e.Fields[f]; !has || fmt.Sprint(v) != want {
				ok = false
				break
			}
		}
		if ok {
			return ctor
		}
	}
	return ""
}

// Undeclared reports whether c is a step's effects of mapped kinds where
// its action has no effects def (Def is empty; Call names the def it
// would take): the model states none, so they fail it, in validate and
// in connect alike (DESIGN §115). An action opted out (effects: "-") is
// never checked.
func (c *EffectCheck) Undeclared() bool { return c != nil && c.Def == "" && len(c.Made) > 0 }

// Check is the comparison of made (a step's effects, in their order)
// with the model's def, applied as call (attemptEffects("c1", "d2")),
// under match (exact, retried or set; "" is exact): each effect of a
// mapped kind rendered by its constructor's templates over its recorded
// fields, the list normalized by match, the rest NotCompared. It is
// validate's plan's and connect's Binding's one normalization, so the
// two cannot judge one step's effects apart. def "" checks an action
// with no effects def: the result is Undeclared if anything mapped was
// made. Templates here have the functions that need no trace (toSet,
// toList, toMap, add, atLeast); a plan's also have its trace's (set,
// max, count, procs) and model's (toQuint).
func (fx *Effects) Check(def, call, match string, made []qtrace.Effect) (*EffectCheck, error) {
	return fx.check(newRenderer(pureFuncs()), def, call, match, made)
}

// check is Check with rd's functions.
func (fx *Effects) check(rd *renderer, def, call, match string, made []qtrace.Effect) (*EffectCheck, error) {
	if match == "" {
		match = MatchExact
	}
	ec := &EffectCheck{Def: def, Match: match, Call: call}
	type one struct {
		v    string
		same []string
		from qtrace.Effect
	}
	var all []one
	for i := range made {
		e := &made[i]
		ctor := fx.constructor(e)
		if ctor == "" {
			ec.NotCompared = append(ec.NotCompared, *e)
			continue
		}
		k := fx.Kinds[ctor]
		data := map[string]any{}
		for f, v := range e.Fields {
			data[f] = fieldValue(v)
		}
		v := ctor
		if len(k.Fields) > 0 {
			parts := make([]string, 0, len(k.Fields))
			for _, f := range slices.Sorted(maps.Keys(k.Fields)) {
				r, err := rd.render(k.Fields[f], data)
				if err != nil {
					if missing, ok := missingField(err); ok {
						return nil, fmt.Errorf("effect %s (seq %d) as %s: field %s reads %q, which the effect does not have (it has: %s): fix effects.kinds.%s.fields.%s",
							e.Kind, e.Seq, ctor, f, missing, strings.Join(slices.Sorted(maps.Keys(e.Fields)), ", "), ctor, f)
					}
					return nil, fmt.Errorf("effect %s (seq %d) as %s: field %s: %w", e.Kind, e.Seq, ctor, f, err)
				}
				parts = append(parts, f+": "+r)
			}
			v = ctor + "({ " + strings.Join(parts, ", ") + " })"
		}
		var same []string
		for _, f := range k.Same {
			same = append(same, fmt.Sprint(e.Fields[f]))
		}
		all = append(all, one{v, same, *e})
	}
	switch match {
	case MatchRetried:
		for i, x := range all {
			if i > 0 && all[i-1].v == x.v {
				prev := all[i-1]
				if slices.Equal(prev.same, x.same) {
					continue // a retry of the same request: one effect
				}
				k := fx.Kinds[fx.constructor(&x.from)]
				for j, f := range k.Same {
					if prev.same[j] != x.same[j] {
						ec.Tries = append(ec.Tries, fmt.Sprintf("effect %s was tried again under another %s: %q (seq %d), then %q (seq %d)", x.v, f, prev.same[j], prev.from.Seq, x.same[j], x.from.Seq))
						break
					}
				}
			}
			ec.Made, ec.From = append(ec.Made, x.v), append(ec.From, x.from)
		}
	case MatchSet:
		seen := map[string]bool{}
		for _, x := range all {
			if !seen[x.v] {
				seen[x.v] = true
				ec.Made, ec.From = append(ec.Made, x.v), append(ec.From, x.from)
			}
		}
		idx := make([]int, len(ec.Made))
		for i := range idx {
			idx[i] = i
		}
		slices.SortStableFunc(idx, func(a, c int) int { return strings.Compare(ec.Made[a], ec.Made[c]) })
		ms, fs := make([]string, len(idx)), make([]qtrace.Effect, len(idx))
		for i, j := range idx {
			ms[i], fs[i] = ec.Made[j], ec.From[j]
		}
		ec.Made, ec.From = ms, fs
	default:
		for _, x := range all {
			ec.Made, ec.From = append(ec.Made, x.v), append(ec.From, x.from)
		}
	}
	if match == MatchSet {
		ec.Literal = "Set(" + strings.Join(ec.Made, ", ") + ")"
	} else {
		ec.Literal = "[" + strings.Join(ec.Made, ", ") + "]"
	}
	return ec, nil
}

// effectCheck is step s's EffectCheck against def, its args the call's
// (by the def's parameters' names), each effect rendered by rd.
func (b *Binding) effectCheck(rd *renderer, m *Model, def, match string, s *qtrace.Step, args map[string]string) (*EffectCheck, []string, error) {
	var defArgs []string
	for _, p := range m.Defs[def] {
		defArgs = append(defArgs, args[p])
	}
	ec, err := b.Effects.check(rd, def, def+"("+strings.Join(defArgs, ", ")+")", match, s.Effects)
	return ec, defArgs, err
}

// effectsConjunct is the expect's conjunct comparing def applied to
// args with made (a list, or for match set a set): Quint text.
func effectsConjunct(def string, args []string, match, made string) string {
	call := def + "(" + strings.Join(args, ", ") + ")"
	if match == MatchSet {
		return fmt.Sprintf("(%s.indices().map(%si => %s[%si]) == %s)", call, evalPrefix, call, evalPrefix, made)
	}
	return fmt.Sprintf("(%s == %s)", call, made)
}

// FirstDifference is where made and expected (an EffectCheck's Made, and
// the def's value as Quint literals) first differ: the effect's index
// (1-based), and a field's name with both values when the two are one
// constructor's records, or "" for a missing or extra effect.
func FirstDifference(made, expected []string) (index int, field, got, want string) {
	for i := range max(len(made), len(expected)) {
		switch {
		case i >= len(made):
			return i + 1, "", "nothing", expected[i]
		case i >= len(expected):
			return i + 1, "", made[i], "nothing"
		case made[i] != expected[i]:
			f, g, w := fieldDiff(made[i], expected[i])
			if f == "" {
				return i + 1, "", made[i], expected[i]
			}
			return i + 1, f, g, w
		}
	}
	return 0, "", "", ""
}

// fieldDiff is the first field of two constructor applications of one
// record ("C({ a: 1, b: 2 })") whose values differ, by the fields'
// sorted order, or "" when they are not of that form.
func fieldDiff(a, b string) (field, x, y string) {
	ca, ra, ok1 := splitCtor(a)
	cb, rb, ok2 := splitCtor(b)
	if !ok1 || !ok2 || ca != cb {
		return "", "", ""
	}
	for _, f := range slices.Sorted(maps.Keys(ra)) {
		if rb[f] != ra[f] {
			return f, ra[f], rb[f]
		}
	}
	for _, f := range slices.Sorted(maps.Keys(rb)) {
		if _, ok := ra[f]; !ok {
			return f, "nothing", rb[f]
		}
	}
	return "", "", ""
}

// splitCtor reads "C({ a: 1, b: "x" })" into C and its fields' texts,
// at the top level only.
func splitCtor(s string) (string, map[string]string, bool) {
	name, rest, ok := strings.Cut(s, "(")
	if !ok || !strings.HasSuffix(rest, ")") {
		return "", nil, false
	}
	body := strings.TrimSpace(strings.TrimSuffix(rest, ")"))
	if !strings.HasPrefix(body, "{") || !strings.HasSuffix(body, "}") {
		return "", nil, false
	}
	body = strings.TrimSpace(body[1 : len(body)-1])
	fields := map[string]string{}
	depth, inStr, start := 0, false, 0
	flush := func(end int) {
		part := strings.TrimSpace(body[start:end])
		if k, v, ok := strings.Cut(part, ":"); ok {
			fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	for i, c := range body {
		switch {
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '(' || c == '{' || c == '[':
			depth++
		case c == ')' || c == '}' || c == ']':
			depth--
		case c == ',' && depth == 0:
			flush(i)
			start = i + 1
		}
	}
	flush(len(body))
	return strings.TrimSpace(name), fields, true
}
