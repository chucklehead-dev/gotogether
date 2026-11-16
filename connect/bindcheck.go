package connect

import (
	"encoding"
	"fmt"
	"math/big"
	"reflect"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/itf"
)

// A Binding is checked against the run's model before any trace (ROADMAP
// CW5's static half): each bound action is one of the model's, each pick
// one of its nondets or the action's parameters, and each Go type one
// that itf.Decode fills from the pick's Quint type, as the model's type
// checker inferred it (the compile JSON's type table). A trace exercises
// an action only when it samples it, so a typo in a name, or a parameter
// type the model's values do not decode into, would otherwise pass every
// run that never took that action. Where the check cannot be sure (a type
// variable, a Go interface, a type that decodes itself) it accepts: a
// refusal of a binding that works is worse than a mismatch found by a
// trace.

// modelChecked is a Binding, or a driver Bind made: Binding.Run and
// Binding.Test check the binding against the run's model before
// generating a trace, connect.Run and connect.Test a driver's before
// replaying the first.
type modelChecked interface {
	// defs are the model definitions the binding names (Returns'),
	// compiled for before the check
	defs() []string
	// checkModel is every mismatch between the binding and m, one line
	// each; unbound, the model's actions no binding takes
	checkModel(m *qeval.Model, c Config) (mismatches, unbound, notes []string)
}

func (b *Binding[T]) defs() []string {
	var out []string
	for _, n := range b.order {
		if a := b.actions[n]; a.returns != "" && !slices.Contains(out, a.returns) {
			out = append(out, a.returns)
		}
	}
	return out
}

func (b *Binding[T]) checkModel(m *qeval.Model, c Config) (mismatches, unbound, notes []string) {
	mismatches, unbound, notes = checkBinding(b.order, b.actions, m, c)
	return append(mismatches, checkState(b.stateType, m, c)...), unbound, notes
}

func (d *boundDriver[T]) defs() []string { return d.b.defs() }

func (d *boundDriver[T]) checkModel(m *qeval.Model, c Config) (mismatches, unbound, notes []string) {
	return d.b.checkModel(m, c)
}

// checkBinding is checkModel's actions; notes, one per bound action
// whose picks are checked against the whole model's, says why, so that
// a pass of a pick it borrows is not read as an attribution's.
func checkBinding(order []string, actions map[string]*boundAction, m *qeval.Model, c Config) (out, unbound, notes []string) {
	bad := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	init, step := c.Init, c.StepName
	if init == "" {
		init = "init"
	}
	if step == "" {
		step = "step"
	}
	// the model's actions, as a trace names them (step and init are not)
	var modelActions []string
	for _, n := range m.Actions() {
		if n != init && n != step {
			modelActions = append(modelActions, n)
		}
	}
	// a NondetPath run reads its actions and picks from a variable, not
	// from the model's actions and nondets: only Returns is checked
	picksKnown := len(c.NondetPath) == 0
	var nondets map[string][]*qeval.Type
	var own map[string]map[string][]*qeval.Type
	var unsure map[string]string
	if picksKnown {
		nondets = m.Nondets(step)
		// each action's own picks (qeval's ActionPicks): quint run --mbt
		// records every nondet at every step, so a pick of another
		// action's decodes that action's choice. Where the step's shape
		// hides which nondets an action takes, its picks are the whole
		// model's (unsure, with why: logged), as before: accepted, not
		// refused
		own, unsure = m.ActionPicks(step)
		picksKnown = nondets != nil
	}
	for _, name := range order {
		a := actions[name]
		var params []qeval.Field
		if picksKnown {
			d, ok := m.Decl(name)
			if !ok || d.Kind != "def" || d.Qualifier != "action" {
				bad("connect.Bind: action %s: the model has no action %s (its actions: %s): bind each action by the model's name for it", name, name, strings.Join(modelActions, ", "))
				continue
			}
			params = m.Params(d)
		}
		if a.handler != nil {
			continue
		}
		mine, ok := own[name]
		if !ok {
			mine = nondets
		}
		// the Quint types a pick of the step takes: the action's own
		// nondet's, else its parameter of that name (nil when neither
		// is known)
		pickTypes := func(pick string) ([]*qeval.Type, bool) {
			if ts, ok := mine[pick]; ok {
				return ts, true
			}
			for _, p := range params {
				if p.Name == pick {
					return []*qeval.Type{p.Type}, true
				}
			}
			return nil, false
		}
		pickNames := func() string {
			var ns []string
			for n := range mine {
				ns = append(ns, n)
			}
			for _, p := range params {
				if !slices.Contains(ns, p.Name) {
					ns = append(ns, p.Name)
				}
			}
			slices.Sort(ns)
			return strings.Join(ns, ", ")
		}
		// borrowed says, of a pick the model chooses but not for this
		// action, whose it is: the step's nondet another action takes
		borrowed := func(pick string) (string, bool) {
			if _, ok := nondets[pick]; !ok {
				return "", false
			}
			var owners []string
			for _, n := range modelActions {
				if _, hidden := unsure[n]; n != name && !hidden {
					if _, ok := own[n][pick]; ok {
						owners = append(owners, n)
					}
				}
			}
			whose := fmt.Sprintf("%s is a pick of %s, not %s", pick, andList(owners), name)
			if len(owners) == 0 {
				whose = fmt.Sprintf("%s is chosen by %s, but no action's call uses it", pick, step)
			}
			return fmt.Sprintf("%s (%s's picks: %s)", whose, name, pickNames()), true
		}
		if why, hidden := unsure[name]; picksKnown && hidden && len(a.params)+len(a.observes)+len(a.returnsArgs) > 0 {
			notes = append(notes, fmt.Sprintf("connect.Bind: picks of %s not attributed (%s): checked against the whole model's", name, why))
		}
		if picksKnown {
			for i, p := range a.params {
				ts, ok := pickTypes(p)
				if why, other := borrowed(p); !ok && other {
					bad("connect.Bind: action %s, parameter %d (pick %s): %s: name a nondet that feeds %s", name, i+1, p, why, name)
					continue
				}
				if !ok {
					bad("connect.Bind: action %s, parameter %d (pick %s): the model has no nondet or parameter %s (its picks: %s): name the nondet that feeds the parameter", name, i+1, p, p, pickNames())
					continue
				}
				// a pointer parameter is an optional pick: its value
				// decodes into what it points to
				t := a.types[i]
				if t.Kind() == reflect.Pointer {
					t = t.Elem()
				}
				if q, why, ok := fitsAny(ts, t); !ok {
					bad("connect.Bind: action %s, parameter %d (pick %s): Go type %s does not fit the model's %s%s: make the parameter a type itf.Decode fills from %s", name, i+1, p, a.types[i], q, why, q)
				}
			}
			for i, p := range a.observes {
				ts, ok := pickTypes(p)
				if why, other := borrowed(p); !ok && other {
					bad("connect.Bind: action %s: Observes' pick %s: %s: name the nondet the model picks %s's choice with", name, p, why, name)
					continue
				}
				if !ok {
					bad("connect.Bind: action %s: Observes' pick %s: the model has no nondet or parameter %s (its picks: %s): name the nondet the model picks the choice with", name, p, p, pickNames())
					continue
				}
				if q, why, ok := fitsAny(ts, a.results[i]); !ok {
					bad("connect.Bind: action %s: Observes' pick %s: result %d, Go type %s, does not fit the model's %s%s: return a type itf.Decode fills from %s", name, p, i+1, a.results[i], q, why, q)
				}
			}
			for _, p := range a.returnsArgs {
				_, ok := pickTypes(p)
				if why, other := borrowed(p); !ok && other {
					bad("connect.Bind: action %s: Returns' argument %s: %s: pass picks of %s", name, p, why, name)
					continue
				}
				if !ok {
					bad("connect.Bind: action %s: Returns' argument %s: the model has no nondet or parameter %s (its picks: %s): pass the picks the def takes", name, p, p, pickNames())
				}
			}
		}
		if a.returns != "" {
			checkReturns(a, m, c, bad)
		}
	}
	for _, n := range modelActions {
		if _, ok := actions[n]; !ok && picksKnown {
			unbound = append(unbound, n)
		}
	}
	return out, unbound, notes
}

// andList is names as prose: a, a and b, a, b and c.
func andList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// checkState checks State's projection type against the model's state,
// as Replay decodes the state into it (itf.DecodeProjection, at
// Config.StatePath): every field the projection has must be a state
// variable's (or a record field's, below the top), of a type Decode
// fills from it, and exported. A projection whose type is an interface
// (a func(T) any) is decoded into the type it returns at the first
// trace's init, and not checked here.
func checkState(t reflect.Type, m *qeval.Model, c Config) []string {
	if t == nil || t.Kind() == reflect.Interface {
		return nil
	}
	// the state as a trace has it: a record of the variables, and of the
	// mbt:: ones a --mbt trace adds (any type)
	q := &qeval.Type{Kind: "rec"}
	for _, v := range m.Vars {
		d, _ := m.Decl(v)
		vt, ok := m.TypeOf(d)
		if !ok {
			return nil // an IR without types: the first init decodes it
		}
		q.Fields = append(q.Fields, qeval.Field{Name: m.Display(v), Type: vt})
	}
	q.Fields = append(q.Fields, qeval.Field{Name: "mbt::actionTaken"}, qeval.Field{Name: "mbt::nondetPicks"})
	// as Replay's walk reads the path: only its first segment names a
	// variable, which a flattened import qualifies (m::balances, the
	// trace's balances); the segments after it name record fields, which
	// carry no qualifier
	for i, p := range c.StatePath {
		var next *qeval.Type
		if q != nil && q.Kind == "rec" {
			for _, f := range q.Fields {
				if f.Name == p || i == 0 && strings.Contains(p, "::") && f.Name == p[strings.LastIndex(p, "::")+2:] {
					next = f.Type
				}
			}
		}
		if next == nil {
			return nil // a path the model's types do not show: Replay names it
		}
		q = next
	}
	f := fitter{seen: map[fitKey]bool{}, strict: true}
	path, why, ok := f.fits(q, t, "")
	if ok {
		return nil
	}
	where := " (" + why + ")"
	if path != "" {
		where = fmt.Sprintf(" (at %s, %s)", path, why)
	}
	state := "state"
	if len(c.StatePath) > 0 {
		state = fmt.Sprintf("state at %s", strings.Join(c.StatePath, "."))
	}
	return []string{fmt.Sprintf("connect.Bind: State: the projection, Go type %s, does not fit the model's %s%s: project the model's state variables, each into a type itf.Decode fills from it", t, state, where)}
}

// checkReturns checks that Returns' def is one of the model's, taking
// the arguments given, and that its value decodes into the result.
func checkReturns(a *boundAction, m *qeval.Model, c Config, bad func(string, ...any)) {
	d, ok := m.Decl(a.returns)
	if ok && d.Kind == "def" {
		switch d.Qualifier {
		case "val", "def", "pureval", "puredef":
		default:
			ok = false
		}
	}
	if !ok || d.Kind != "def" {
		bad("connect.Bind: action %s: Returns' def %s is no val or def of %s: name the model definition the result must equal", a.name, a.returns, c.Spec)
		return
	}
	params := m.Params(d)
	if len(params) != len(a.returnsArgs) {
		var ps []string
		for _, p := range params {
			ps = append(ps, p.Name)
		}
		bad("connect.Bind: action %s: Returns' def %s takes %d argument%s (%s), but %d pick%s given: name one pick per parameter, in order", a.name, a.returns, len(params), plural(len(params)), strings.Join(ps, ", "), len(a.returnsArgs), choose(len(a.returnsArgs) == 1, " is", "s are"))
		return
	}
	t, ok := m.TypeOf(d)
	if !ok {
		return
	}
	if t.Kind == "oper" {
		t = t.Res
	}
	if q, why, ok := fitsAny([]*qeval.Type{t}, a.results[0]); !ok {
		bad("connect.Bind: action %s: its result, Go type %s, does not fit Returns' def %s, of the model's %s%s: return a type itf.Decode fills from %s", a.name, a.results[0], a.returns, q, why, q)
	}
}

// fitsAny reports whether Go type t fits one of the Quint types ts (a
// nondet one name chooses at two types fits if either does); when none
// does, the first type and why.
func fitsAny(ts []*qeval.Type, t reflect.Type) (*qeval.Type, string, bool) {
	var why string
	for i, q := range ts {
		f := fitter{seen: map[fitKey]bool{}}
		path, w, ok := f.fits(q, t, "")
		if ok {
			return nil, "", true
		}
		if i == 0 {
			why = w
			if path != "" {
				why = fmt.Sprintf(" (at %s, %s)", path, w)
			} else if w != "" {
				why = " (" + w + ")"
			}
		}
	}
	if len(ts) == 0 {
		return nil, "", true
	}
	return ts[0], why, false
}

var (
	unmarshalerIface = reflect.TypeFor[itf.Unmarshaler]()
	itfValueType     = reflect.TypeFor[itf.Value]()
	bigIntValueType  = reflect.TypeFor[big.Int]()

	textUnmarshalerIface = reflect.TypeFor[encoding.TextUnmarshaler]()
)

type fitKey struct {
	q *qeval.Type
	t reflect.Type
}

// fitter mirrors itf.Decode's rules (itf/decode.go): fits reports
// whether every value of Quint type q decodes into Go type t, and if
// not, where and why. seen breaks the cycles of a recursive Go type.
// strict is itf.DecodeProjection's rules, a state projection's: a
// struct's fields must be exported, and a record's fields.
type fitter struct {
	seen   map[fitKey]bool
	strict bool
}

func (f fitter) fits(q *qeval.Type, t reflect.Type, path string) (string, string, bool) {
	if q == nil {
		return "", "", true
	}
	k := fitKey{q, t}
	if f.seen[k] {
		return "", "", true
	}
	f.seen[k] = true
	no := func(format string, a ...any) (string, string, bool) {
		return path, fmt.Sprintf(format, a...), false
	}
	mismatch := func() (string, string, bool) { return no("%s does not decode into %s", q, t) }
	// a type that decodes itself, the Value itself, an interface (any
	// takes every value; another, the check cannot know)
	if reflect.PointerTo(t).Implements(unmarshalerIface) || t == itfValueType || t.Kind() == reflect.Interface {
		return "", "", true
	}
	switch q.Kind {
	case "int", "bool", "str", "set", "list", "fun", "tup", "rec", "sum":
	default: // a type variable, an uninterpreted type: any value may come
		return "", "", true
	}
	if t == bigIntValueType {
		if q.Kind != "int" {
			return mismatch()
		}
		return "", "", true
	}
	switch t.Kind() {
	case reflect.Pointer:
		// Option: None is nil, Some(x) is &x; any other value decodes
		// into what the pointer points to
		if some, ok := q.Option(); ok {
			return f.fits(some, t.Elem(), path)
		}
		if q.Kind == "sum" && slices.ContainsFunc(q.Fields, func(v qeval.Field) bool { return v.Name == "Some" || v.Name == "None" }) {
			return "", "", true // Some or None among other variants: unsure
		}
		return f.fits(q, t.Elem(), path)
	case reflect.Bool:
		if q.Kind != "bool" {
			return mismatch()
		}
	case reflect.String:
		switch q.Kind {
		case "str":
		case "sum": // an enum: every variant nullary
			for _, v := range q.Fields {
				if !v.Type.Nullary() {
					return no("variant %s carries %s, which %s cannot hold: decode it into a struct with fields tagged `itf:\",tag\"` and `itf:\",value\"`", v.Name, v.Type, t)
				}
			}
		default:
			return mismatch()
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		if q.Kind != "int" {
			return mismatch()
		}
	case reflect.Slice:
		switch q.Kind {
		case "str":
			if t.Elem().Kind() != reflect.Uint8 {
				return mismatch()
			}
		case "fun": // a map into a slice of its pairs, sorted by key
			pair := &qeval.Type{Kind: "tup", Fields: []qeval.Field{{Name: "0", Type: q.Arg}, {Name: "1", Type: q.Res}}}
			return f.fits(pair, t.Elem(), path+"[i]")
		case "set", "list":
			return f.fits(q.Elem, t.Elem(), path+"[i]")
		case "tup":
			for i, e := range q.Fields {
				if p, w, ok := f.fits(e.Type, t.Elem(), fmt.Sprintf("%s[%d]", path, i)); !ok {
					return p, w, ok
				}
			}
		default:
			return mismatch()
		}
	case reflect.Array:
		switch q.Kind {
		case "set", "list":
			return f.fits(q.Elem, t.Elem(), path+"[i]")
		case "tup":
			if len(q.Fields) != t.Len() && !q.Open {
				return no("a tuple of %d elements does not decode into %s", len(q.Fields), t)
			}
			for i, e := range q.Fields {
				if p, w, ok := f.fits(e.Type, t.Elem(), fmt.Sprintf("%s[%d]", path, i)); !ok {
					return p, w, ok
				}
			}
		default:
			return mismatch()
		}
	case reflect.Map:
		switch q.Kind {
		case "fun":
			if p, w, ok := f.fits(q.Arg, t.Key(), path+"{key}"); !ok {
				return p, w, ok
			}
			return f.fits(q.Res, t.Elem(), path+"[k]")
		case "rec":
			if t.Key().Kind() != reflect.String {
				return no("a record decodes into a map with string keys, not %s", t)
			}
			for _, fl := range q.Fields {
				if p, w, ok := f.fits(fl.Type, t.Elem(), path+"."+fl.Name); !ok {
					return p, w, ok
				}
			}
		case "set": // membership: map[T]bool or map[T]struct{}
			if ek := t.Elem().Kind(); ek != reflect.Bool && !(ek == reflect.Struct && t.Elem().NumField() == 0) {
				return no("a set decodes into map[T]bool or map[T]struct{}, not %s", t)
			}
			return f.fits(q.Elem, t.Key(), path+"{key}")
		default:
			return mismatch()
		}
	case reflect.Struct:
		return f.fitsStruct(q, t, path, mismatch)
	default:
		if q.Kind == "str" && reflect.PointerTo(t).Implements(textUnmarshalerIface) {
			return "", "", true
		}
		return mismatch()
	}
	return "", "", true
}

// fitsStruct is decodeStruct's rules: a tuple fills the fields in order,
// a record the fields named like its own, a variant its tag and value
// fields or the field named like the variant.
func (f fitter) fitsStruct(q *qeval.Type, t reflect.Type, path string, mismatch func() (string, string, bool)) (string, string, bool) {
	if f.strict { // never decoded, never compared
		for i := range t.NumField() {
			if sf := t.Field(i); !sf.IsExported() {
				return path, fmt.Sprintf("field %s of %s is unexported, so it is never compared: export it, or leave it out of the type", sf.Name, t), false
			}
		}
	}
	fields := goFields(t)
	tupleTagged := slices.ContainsFunc(fields, func(g goField) bool { return g.tuple })
	var live []goField
	for _, g := range fields {
		if !g.skip {
			live = append(live, g)
		}
	}
	switch q.Kind {
	case "tup":
		if len(live) != len(q.Fields) && !q.Open {
			return path, fmt.Sprintf("a tuple of %d elements does not decode into %s, of %d fields", len(q.Fields), t, len(live)), false
		}
		for i, e := range q.Fields {
			if i >= len(live) {
				break
			}
			if p, w, ok := f.fits(e.Type, t.Field(live[i].index).Type, fmt.Sprintf("%s._%d", path, i+1)); !ok {
				return p, w, ok
			}
		}
	case "list":
		if !tupleTagged {
			return mismatch()
		}
		for i, g := range live {
			if p, w, ok := f.fits(q.Elem, t.Field(g.index).Type, fmt.Sprintf("%s._%d", path, i+1)); !ok {
				return p, w, ok
			}
		}
	case "rec":
		for _, g := range fields {
			if g.skip || g.tag || g.value {
				continue
			}
			found := false
			for _, fl := range q.Fields {
				if fl.Name == g.name || normName(fl.Name) == normName(g.name) {
					if p, w, ok := f.fits(fl.Type, t.Field(g.index).Type, path+"."+fl.Name); !ok {
						return p, w, ok
					}
					found = true
					break
				}
			}
			// a field the record lacks keeps its zero value, as Decode
			// leaves it; DecodeProjection refuses it, since it would
			// compare nothing
			if !found && f.strict && !q.Open {
				var names []string
				for _, fl := range q.Fields {
					if !strings.HasPrefix(fl.Name, "mbt::") {
						names = append(names, fl.Name)
					}
				}
				slices.Sort(names)
				return path, fmt.Sprintf("field %s of %s is not the model's (it has %s): name it after one, or tag it `itf:\"-\"`", g.name, t, strings.Join(names, ", ")), false
			}
		}
	case "sum":
		var tagF, valF *goField
		for i := range fields {
			if fields[i].tag {
				tagF = &fields[i]
			}
			if fields[i].value {
				valF = &fields[i]
			}
		}
		if tagF == nil {
			for i := range fields {
				if fields[i].name == "Tag" || fields[i].name == "tag" {
					tagF = &fields[i]
				}
			}
		}
		str := &qeval.Type{Kind: "str"}
		for _, v := range q.Fields {
			if tagF != nil {
				if p, w, ok := f.fits(str, t.Field(tagF.index).Type, path+".tag"); !ok {
					return p, w, ok
				}
			}
			if valF != nil {
				if v.Type.Nullary() {
					continue
				}
				if p, w, ok := f.fits(v.Type, t.Field(valF.index).Type, path+"."+v.Name); !ok {
					return p, w, ok
				}
				continue
			}
			found := false
			for _, g := range fields {
				if g.skip || g.tag || !(g.name == v.Name || normName(g.name) == normName(v.Name)) {
					continue
				}
				found = true
				ft := t.Field(g.index).Type
				if v.Type.Nullary() && ft.Kind() == reflect.Bool {
					break
				}
				payload := v.Type
				if payload.Nullary() {
					payload = &qeval.Type{Kind: "tup"}
				}
				if p, w, ok := f.fits(payload, ft, path+"."+v.Name); !ok {
					return p, w, ok
				}
				break
			}
			if !found && !(tagF != nil && v.Type.Nullary()) {
				return path, fmt.Sprintf("variant %s has no field in %s: give it a field named %s, or fields tagged `itf:\",tag\"` and `itf:\",value\"`", v.Name, t, v.Name), false
			}
		}
	default:
		return mismatch()
	}
	return "", "", true
}

// goField is a struct field as itf reads it (itf's structFields).
type goField struct {
	index                   int
	name                    string
	tag, value, tuple, skip bool
}

func goFields(t reflect.Type) []goField {
	var out []goField
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		g := goField{index: i, name: sf.Name}
		tag, ok := sf.Tag.Lookup("itf")
		if !ok {
			if jt, ok := sf.Tag.Lookup("json"); ok {
				tag, _, _ = strings.Cut(jt, ",")
			}
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "-" {
			g.skip = true
		} else if name != "" {
			g.name = name
		}
		for o := range strings.SplitSeq(opts, ",") {
			switch o {
			case "tag":
				g.tag = true
			case "value":
				g.value = true
			case "tuple":
				g.tuple = true
			}
		}
		out = append(out, g)
	}
	return out
}

// normName is itf's field-name normalisation: lower case, no underscores.
func normName(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", ""))
}
