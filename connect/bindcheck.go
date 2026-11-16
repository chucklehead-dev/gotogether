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

// modelChecked is a driver Bind made: connect.Run and connect.Test check
// its binding against the run's model before replaying the first trace.
type modelChecked interface {
	// defs are the model definitions the binding names (Returns'),
	// compiled for before the check
	defs() []string
	// checkModel is every mismatch between the binding and m, one line
	// each; unbound, the model's actions no binding takes
	checkModel(m *qeval.Model, c Config) (mismatches, unbound []string)
}

func (d *boundDriver[T]) defs() []string {
	var out []string
	for _, n := range d.b.order {
		if a := d.b.actions[n]; a.returns != "" && !slices.Contains(out, a.returns) {
			out = append(out, a.returns)
		}
	}
	return out
}

func (d *boundDriver[T]) checkModel(m *qeval.Model, c Config) (mismatches, unbound []string) {
	return checkBinding(d.b.order, d.b.actions, m, c)
}

func checkBinding(order []string, actions map[string]*boundAction, m *qeval.Model, c Config) (out, unbound []string) {
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
	if picksKnown {
		nondets = m.Nondets(step)
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
		// the Quint types a pick of the step takes: its nondet's, else
		// the model's parameter of that name (nil when neither is known)
		pickTypes := func(pick string) ([]*qeval.Type, bool) {
			if ts, ok := nondets[pick]; ok {
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
			for n := range nondets {
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
		if picksKnown {
			for i, p := range a.params {
				ts, ok := pickTypes(p)
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
				if !ok {
					bad("connect.Bind: action %s: Observes' pick %s: the model has no nondet or parameter %s (its picks: %s): name the nondet the model picks the choice with", name, p, p, pickNames())
					continue
				}
				if q, why, ok := fitsAny(ts, a.results[i]); !ok {
					bad("connect.Bind: action %s: Observes' pick %s: result %d, Go type %s, does not fit the model's %s%s: return a type itf.Decode fills from %s", name, p, i+1, a.results[i], q, why, q)
				}
			}
			for _, p := range a.returnsArgs {
				if _, ok := pickTypes(p); !ok {
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
	return out, unbound
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
		bad("connect.Bind: action %s: Returns' def %s takes %d argument%s (%s), but %d pick%s given: name one pick per parameter, in order", a.name, a.returns, len(params), plural(len(params)), strings.Join(ps, ", "), len(a.returnsArgs), map[bool]string{true: " is", false: "s are"}[len(a.returnsArgs) == 1])
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
type fitter struct{ seen map[fitKey]bool }

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
			for _, fl := range q.Fields {
				if fl.Name == g.name || normName(fl.Name) == normName(g.name) {
					if p, w, ok := f.fits(fl.Type, t.Field(g.index).Type, path+"."+fl.Name); !ok {
						return p, w, ok
					}
					break
				}
			}
			// a field the record lacks keeps its zero value, as Decode leaves it
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
