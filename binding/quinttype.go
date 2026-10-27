package binding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/chucklehead-dev/gotogether/itf"
)

// A qtype is a Quint type, its aliases resolved: what toQuint decodes a
// step's JSON by.
type qtype struct {
	kind   string   // "int", "str", "bool", "set", "list", "map", "tup", "rec", "sum", "alias"
	elem   *qtype   // set, list; map's key
	val    *qtype   // map's value
	fields []qfield // tup (names "0", "1", ...), rec, sum (a tag and its payload: an empty tup for none)
	name   string   // alias: its name, as written (resolved in target)
	target *qtype   // alias: what it stands for
}

type qfield struct {
	name string
	typ  *qtype
	bare bool // parsing: a tag written alone, which may be an alias the model lacks
}

// resolved is t with its alias links followed.
func (t *qtype) resolved() *qtype {
	for t != nil && t.kind == "alias" {
		t = t.target
	}
	return t
}

// String is t in the syntax typeOf parses: aliases by name, a sum as its
// alternatives (`A(int) | B`), a sum or a map parenthesized as a map's key.
func (t *qtype) String() string { return t.render(false) }

func (t *qtype) render(nested bool) string {
	switch t.kind {
	case "int", "str", "bool":
		return t.kind
	case "alias":
		return t.name
	case "set":
		return "Set[" + t.elem.render(false) + "]"
	case "list":
		return "List[" + t.elem.render(false) + "]"
	case "map":
		s := t.elem.render(true) + " -> " + t.val.render(false)
		if nested { // a key: -> groups to the right
			s = "(" + s + ")"
		}
		return s
	case "tup":
		parts := make([]string, len(t.fields))
		for i, f := range t.fields {
			parts[i] = f.typ.render(false)
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case "rec":
		parts := make([]string, len(t.fields))
		for i, f := range t.fields {
			parts[i] = f.name + ": " + f.typ.render(false)
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	case "sum":
		parts := make([]string, len(t.fields))
		for i, f := range t.fields {
			parts[i] = f.name
			if p := f.typ.resolved(); p == nil || !(p.kind == "tup" && len(p.fields) == 0) { // nil: an alias unresolved, printed by name
				parts[i] += "(" + f.typ.render(false) + ")"
			}
		}
		s := strings.Join(parts, " | ")
		if nested {
			s = "(" + s + ")"
		}
		return s
	}
	return "?"
}

// structured reports whether t holds a record or a sum, or a collection
// toSet, toList and toMap do not render (their elements scalars or tuples
// of scalars, a map's keys strings and its values scalars): a parameter
// of such a type defaults to toQuint.
func (t *qtype) structured() bool {
	scalar := func(t *qtype) bool {
		t = t.resolved()
		return t.kind == "int" || t.kind == "str" || t.kind == "bool"
	}
	flat := func(t *qtype) bool {
		t = t.resolved()
		if scalar(t) {
			return true
		}
		if t.kind != "tup" {
			return false
		}
		for _, f := range t.fields {
			if !scalar(f.typ) {
				return false
			}
		}
		return true
	}
	switch r := t.resolved(); r.kind {
	case "int", "str", "bool":
		return false
	case "set", "list":
		return !flat(r.elem)
	case "map":
		return r.elem.resolved().kind != "str" || !scalar(r.val)
	}
	return true
}

// typedef is a model's type alias: its parameters and definition (IR).
type typedef struct {
	params []string
	def    json.RawMessage
}

// irType converts a type's JSON IR (quint typecheck's), aliases looked up
// in defs, type variables in vars.
func irType(raw json.RawMessage, defs map[string]typedef, vars map[string]*qtype, depth int) (*qtype, error) {
	if depth > 50 {
		return nil, fmt.Errorf("a type nested past 50 levels (a recursive alias?)")
	}
	var t struct {
		Kind   string          `json:"kind"`
		Name   string          `json:"name"`
		Elem   json.RawMessage `json:"elem"`
		Arg    json.RawMessage `json:"arg"`
		Res    json.RawMessage `json:"res"`
		Fields struct {
			Fields []struct {
				Name string          `json:"fieldName"`
				Type json.RawMessage `json:"fieldType"`
			} `json:"fields"`
		} `json:"fields"`
		Args []json.RawMessage `json:"args"` // an alias's application
	}
	if len(raw) == 0 || json.Unmarshal(raw, &t) != nil {
		return nil, fmt.Errorf("a type the model's IR does not give")
	}
	sub := func(r json.RawMessage) (*qtype, error) { return irType(r, defs, vars, depth+1) }
	switch t.Kind {
	case "int", "str", "bool":
		return &qtype{kind: t.Kind}, nil
	case "set", "list":
		e, err := sub(t.Elem)
		if err != nil {
			return nil, err
		}
		return &qtype{kind: t.Kind, elem: e}, nil
	case "fun":
		k, err := sub(t.Arg)
		if err != nil {
			return nil, err
		}
		v, err := sub(t.Res)
		if err != nil {
			return nil, err
		}
		return &qtype{kind: "map", elem: k, val: v}, nil
	case "tup", "rec", "sum":
		q := &qtype{kind: t.Kind}
		for _, f := range t.Fields.Fields {
			ft, err := sub(f.Type)
			if err != nil {
				return nil, err
			}
			q.fields = append(q.fields, qfield{name: f.Name, typ: ft})
		}
		if t.Kind == "tup" {
			sort.SliceStable(q.fields, func(i, j int) bool { return fieldIndex(q.fields[i].name) < fieldIndex(q.fields[j].name) })
		}
		return q, nil
	case "var":
		if v, ok := vars[t.Name]; ok {
			return v, nil
		}
		return nil, fmt.Errorf("the type variable %s, unbound (a polymorphic parameter: annotate it)", t.Name)
	case "const":
		var args []*qtype
		for _, a := range t.Args {
			at, err := sub(a)
			if err != nil {
				return nil, err
			}
			args = append(args, at)
		}
		return aliasType(t.Name, args, defs, depth)
	}
	return nil, fmt.Errorf("the type kind %q", t.Kind)
}

func fieldIndex(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 1 << 30
	}
	return n
}

// aliasType is the alias name applied to args. Without defs (nil) it is
// the name alone, unresolved: for printing.
func aliasType(name string, args []*qtype, defs map[string]typedef, depth int) (*qtype, error) {
	if defs == nil {
		return &qtype{kind: "alias", name: written(name, args)}, nil
	}
	d, ok := defs[name]
	if !ok {
		names := make([]string, 0, len(defs))
		for n := range defs {
			names = append(names, n)
		}
		sort.Strings(names)
		known := "none"
		if len(names) > 0 {
			known = strings.Join(names, ", ")
		}
		return nil, fmt.Errorf("the type %s, which the model does not declare (its types: %s)", name, known)
	}
	if len(args) != len(d.params) {
		return nil, fmt.Errorf("the type %s takes %d parameters (%s); given %d", name, len(d.params), strings.Join(d.params, ", "), len(args))
	}
	vars := map[string]*qtype{}
	for i, p := range d.params {
		vars[p] = args[i]
	}
	target, err := irType(d.def, defs, vars, depth+1)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &qtype{kind: "alias", name: written(name, args), target: target}, nil
}

// written is an alias applied to args as Quint writes it: Pair[int].
func written(name string, args []*qtype) string {
	if len(args) == 0 {
		return name
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.String()
	}
	return name + "[" + strings.Join(parts, ", ") + "]"
}

// ---- a type's text ----------------------------------------------------------

// typeOf parses a Quint type as toQuint's first argument writes it:
//
//	int  str  bool  Set[T]  List[T]  K -> V  (T1, T2, ...)  { f: T, ... }
//	Alias  Alias[T, ...]  A(T) | B | ...
//
// an alias is the model's (defs), and a sum is either an alias or its
// alternatives written out.
func typeOf(src string, defs map[string]typedef) (*qtype, error) {
	p := &typeParser{src: src, defs: defs}
	t, err := p.typ()
	if err == nil {
		p.ws()
		if p.i < len(p.src) {
			err = p.errf("%q after the type", p.src[p.i:])
		}
	}
	if err != nil {
		return nil, fmt.Errorf("the type %q: %w", src, err)
	}
	return t, nil
}

type typeParser struct {
	src  string
	i    int
	defs map[string]typedef
}

func (p *typeParser) errf(f string, a ...any) error {
	return fmt.Errorf("at offset %d: %s", p.i, fmt.Sprintf(f, a...))
}

func (p *typeParser) ws() {
	for p.i < len(p.src) && unicode.IsSpace(rune(p.src[p.i])) {
		p.i++
	}
}

func (p *typeParser) eat(tok string) bool {
	p.ws()
	if strings.HasPrefix(p.src[p.i:], tok) {
		p.i += len(tok)
		return true
	}
	return false
}

func (p *typeParser) ident() string {
	p.ws()
	j := p.i
	for j < len(p.src) && (p.src[j] == '_' || unicode.IsLetter(rune(p.src[j])) || unicode.IsDigit(rune(p.src[j]))) {
		j++
	}
	s := p.src[p.i:j]
	p.i = j
	return s
}

// typ is a type: a primary, then -> (a map, its value a type) or | (a
// sum's further alternatives).
func (p *typeParser) typ() (*qtype, error) {
	start := p.i
	t, variant, err := p.primary()
	if err != nil {
		return nil, err
	}
	p.ws()
	if variant != nil && variant.bare && !strings.HasPrefix(p.src[p.i:], "|") {
		// a name alone is an alias, which the model lacks
		_, err := aliasType(variant.name, nil, p.defs, 0)
		return nil, err
	}
	if p.eat("->") {
		v, err := p.typ()
		if err != nil {
			return nil, err
		}
		return &qtype{kind: "map", elem: t, val: v}, nil
	}
	p.ws()
	if !strings.HasPrefix(p.src[p.i:], "|") && variant == nil {
		return t, nil
	}
	if variant == nil {
		p.i = start
		return nil, p.errf("a sum's alternative is a tag or Tag(T), not %s", t)
	}
	variant.bare = false
	sum := &qtype{kind: "sum", fields: []qfield{*variant}}
	for p.eat("|") {
		_, v, err := p.primary()
		if err != nil {
			return nil, err
		}
		if v == nil {
			return nil, p.errf("a sum's alternative is a tag or Tag(T)")
		}
		v.bare = false
		sum.fields = append(sum.fields, *v)
	}
	return sum, nil
}

// primary is one type, or a sum's alternative (variant: its tag and
// payload), which a bare capitalized name is when the model has no
// alias of that name.
func (p *typeParser) primary() (*qtype, *qfield, error) {
	p.ws()
	if p.eat("(") {
		var elems []*qtype
		if !p.eat(")") {
			for {
				t, err := p.typ()
				if err != nil {
					return nil, nil, err
				}
				elems = append(elems, t)
				if p.eat(")") {
					break
				}
				if !p.eat(",") {
					return nil, nil, p.errf("\",\" or \")\" in a tuple")
				}
			}
		}
		if len(elems) == 1 {
			return elems[0], nil, nil // a type in parentheses
		}
		q := &qtype{kind: "tup"}
		for i, e := range elems {
			q.fields = append(q.fields, qfield{name: strconv.Itoa(i), typ: e})
		}
		return q, nil, nil
	}
	if p.eat("{") {
		q := &qtype{kind: "rec"}
		for !p.eat("}") {
			name := p.ident()
			if name == "" {
				return nil, nil, p.errf("a record's field name")
			}
			if !p.eat(":") {
				return nil, nil, p.errf("\":\" after the field %s", name)
			}
			t, err := p.typ()
			if err != nil {
				return nil, nil, err
			}
			q.fields = append(q.fields, qfield{name: name, typ: t})
			if !p.eat(",") && !p.eat("}") {
				return nil, nil, p.errf("\",\" or \"}\" in a record")
			} else if strings.HasSuffix(p.src[:p.i], "}") {
				break
			}
		}
		sort.Slice(q.fields, func(i, j int) bool { return q.fields[i].name < q.fields[j].name })
		return q, nil, nil
	}
	name := p.ident()
	switch name {
	case "":
		if p.i < len(p.src) {
			return nil, nil, p.errf("%q where a type goes", p.src[p.i:p.i+1])
		}
		return nil, nil, p.errf("the end where a type goes")
	case "int", "str", "bool":
		return &qtype{kind: name}, nil, nil
	case "Set", "List":
		if !p.eat("[") {
			return nil, nil, p.errf("\"[\" after %s", name)
		}
		e, err := p.typ()
		if err != nil {
			return nil, nil, err
		}
		if !p.eat("]") {
			return nil, nil, p.errf("\"]\" after %s's element", name)
		}
		return &qtype{kind: strings.ToLower(name), elem: e}, nil, nil
	}
	p.ws()
	if strings.HasPrefix(p.src[p.i:], "(") { // Tag(T): an alternative
		p.i++
		t, err := p.typ()
		if err != nil {
			return nil, nil, err
		}
		if !p.eat(")") {
			return nil, nil, p.errf("\")\" after %s's payload", name)
		}
		return nil, &qfield{name: name, typ: t}, nil
	}
	var args []*qtype
	if p.eat("[") {
		for {
			t, err := p.typ()
			if err != nil {
				return nil, nil, err
			}
			args = append(args, t)
			if p.eat("]") {
				break
			}
			if !p.eat(",") {
				return nil, nil, p.errf("\",\" or \"]\" in %s's parameters", name)
			}
		}
	}
	if _, ok := p.defs[name]; !ok && args == nil && unicode.IsUpper(rune(name[0])) {
		return nil, &qfield{name: name, typ: &qtype{kind: "tup"}, bare: true}, nil // a nullary tag
	}
	t, err := aliasType(name, args, p.defs, 0)
	if err != nil {
		return nil, nil, err
	}
	return t, nil, nil
}

// typeOf is src parsed against the model's aliases (none without a
// model), cached in the model.
func (m *Model) typeOf(src string) (*qtype, error) {
	if m == nil {
		return typeOf(src, map[string]typedef{})
	}
	if m.types != nil {
		if v, ok := m.types.Load(src); ok {
			if err, bad := v.(error); bad {
				return nil, err
			}
			return v.(*qtype), nil
		}
	}
	defs := m.typedefs
	if defs == nil { // a Model made by hand: no aliases
		defs = map[string]typedef{}
	}
	t, err := typeOf(src, defs)
	if m.types != nil {
		if err != nil {
			m.types.Store(src, err)
		} else {
			m.types.Store(src, t)
		}
	}
	return t, err
}

// ---- toQuint ----------------------------------------------------------------

// toQuint renders a step's field as a Quint value of the model's type
// src: the field's JSON (qobs records a collection or a struct's
// fields as JSON text) decoded by the type into an itf.Value, printed as
// Quint. Records are JSON objects (a key matching a field exactly, or
// ignoring case and underscores: Go's field names), every field present
// and no other; a sum's value is "Tag", {"tag": "Tag", "value": v} (ITF's),
// {"Tag": v}, or {"tag": "Tag", ...the payload record's fields}, and null is
// None where the sum has one; a map is an object (its keys parsed by the
// key's type) or a list of [key, value] pairs; a tuple is an array. A
// number with a fraction or an exponent, and a time, are refused, naming
// where: Quint has neither, so record an integer unit.
func toQuint(m *Model, src string, v any) (string, error) {
	t, err := m.typeOf(src)
	if err != nil {
		return "", fmt.Errorf("toQuint: %w", err)
	}
	j, err := jsonOf(v, t)
	if err != nil {
		return "", fmt.Errorf("toQuint %s: %w", src, err)
	}
	val, err := decodeAs(j, t, "")
	if err != nil {
		return "", fmt.Errorf("toQuint %s: %w", src, err)
	}
	return val.String(), nil
}

// jsonOf is a template value as decoded JSON: a recorded string is JSON
// text for a type that is no str (a collection, a struct), itself for a
// str; an integer, a boolean, a float are themselves.
func jsonOf(v any, t *qtype) (any, error) {
	switch x := v.(type) {
	case qfloat:
		return json.Number(strconv.FormatFloat(float64(x), 'g', -1, 64)), nil
	case qlit:
		s := string(x)
		if u, err := strconv.Unquote(s); err == nil && strings.HasPrefix(s, `"`) {
			v = u
		} else {
			v = json.Number(s) // an integer or a boolean literal
			if s == "true" || s == "false" {
				v = s == "true"
			}
		}
	case int64:
		v = json.Number(strconv.FormatInt(x, 10))
	case int:
		v = json.Number(strconv.Itoa(x))
	case float64:
		v = json.Number(strconv.FormatFloat(x, 'g', -1, 64))
	}
	s, ok := v.(string)
	if !ok || t.resolved().kind == "str" {
		return v, nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		if t.resolved().kind == "sum" {
			return s, nil // a tag, recorded as a string: "None"
		}
		return nil, fmt.Errorf("the field holds %q, which is not JSON: record %s as JSON (qobs records a slice or a map as JSON, a struct by fmt.Sprint: pass a struct's json.Marshal text, or a slice of it)", s, t)
	}
	if dec.More() {
		return nil, fmt.Errorf("the field holds %q, more than one JSON value", s)
	}
	return out, nil
}

// decodeAs decodes JSON value j (UseNumber's) as t; at is its path, for
// errors.
func decodeAs(j any, t *qtype, at string) (itf.Value, error) {
	where := func() string {
		if at == "" {
			return "the value"
		}
		return at
	}
	fail := func(f string, a ...any) (itf.Value, error) {
		return itf.Value{}, fmt.Errorf("at %s: %s", where(), fmt.Sprintf(f, a...))
	}
	r := t.resolved()
	if j == nil && (r.kind == "set" || r.kind == "list" || r.kind == "map") {
		j = []any{} // Go's nil slice or map: empty
	}
	if r.kind != "sum" && j == nil {
		return fail("null where %s goes (record a value, or type it with an Option-like sum whose None stands for none)", t)
	}
	switch r.kind {
	case "int":
		switch n := j.(type) {
		case json.Number:
			if strings.ContainsAny(string(n), ".eE") {
				return fail("%s is not an integer: Quint has no fractions; record an integer unit (cents, milliseconds) and type the field int", n)
			}
			i, ok := new(big.Int).SetString(string(n), 10)
			if !ok {
				return fail("%s is not an integer", n)
			}
			return itf.Value{Kind: itf.Int, I: i}, nil
		case string:
			if isTime(n) {
				return fail("%q is a time: Quint has none; record an integer unit (time.UnixMilli) and type the field int", n)
			}
		}
		return fail("%s where an int goes", jsonText(j))
	case "str":
		s, ok := j.(string)
		if !ok {
			return fail("%s where a str goes", jsonText(j))
		}
		if isTime(s) {
			return fail("%q is a time: Quint has none; record an integer unit (time.UnixMilli) and type the field int", s)
		}
		return itf.StrOf(s), nil
	case "bool":
		b, ok := j.(bool)
		if !ok {
			return fail("%s where a bool goes", jsonText(j))
		}
		return itf.BoolOf(b), nil
	case "set", "list":
		xs, ok := j.([]any)
		if !ok {
			return fail("%s where %s goes (a JSON array)", jsonText(j), t)
		}
		out := itf.Value{Kind: itf.List}
		if r.kind == "set" {
			out.Kind = itf.Set
		}
		seen := map[string]bool{}
		for i, x := range xs {
			e, err := decodeAs(x, r.elem, fmt.Sprintf("%s[%d]", at, i))
			if err != nil {
				return itf.Value{}, err
			}
			if r.kind == "set" {
				if seen[e.String()] {
					continue
				}
				seen[e.String()] = true
			}
			out.Elems = append(out.Elems, e)
		}
		return out, nil
	case "tup":
		xs, ok := j.([]any)
		if !ok || len(xs) != len(r.fields) {
			return fail("%s where %s goes (a JSON array of %d)", jsonText(j), t, len(r.fields))
		}
		out := itf.Value{Kind: itf.Tuple}
		for i, x := range xs {
			e, err := decodeAs(x, r.fields[i].typ, fmt.Sprintf("%s[%d]", at, i))
			if err != nil {
				return itf.Value{}, err
			}
			out.Elems = append(out.Elems, e)
		}
		return out, nil
	case "map":
		out := itf.Value{Kind: itf.Map}
		add := func(k, v any, path string) error {
			kv, err := decodeAs(k, r.elem, path+"(key)")
			if err != nil {
				return err
			}
			vv, err := decodeAs(v, r.val, path)
			if err != nil {
				return err
			}
			out.Pairs = append(out.Pairs, [2]itf.Value{kv, vv})
			return nil
		}
		switch x := j.(type) {
		case map[string]any:
			for _, k := range sortedKeys(x) {
				var key any = k
				if r.elem.resolved().kind == "int" {
					key = json.Number(k)
				} else if r.elem.resolved().kind != "str" {
					return fail("an object's keys are strings; a map keyed by %s is a list of [key, value] pairs", r.elem)
				}
				if err := add(key, x[k], fmt.Sprintf("%s[%q]", at, k)); err != nil {
					return itf.Value{}, err
				}
			}
		case []any:
			for i, p := range x {
				kv, ok := p.([]any)
				if !ok || len(kv) != 2 {
					return fail("[%d] is %s, not a [key, value] pair", i, jsonText(p))
				}
				if err := add(kv[0], kv[1], fmt.Sprintf("%s[%d]", at, i)); err != nil {
					return itf.Value{}, err
				}
			}
		default:
			return fail("%s where %s goes (a JSON object, or a list of [key, value] pairs)", jsonText(j), t)
		}
		return out, nil
	case "rec":
		obj, ok := j.(map[string]any)
		if !ok {
			return fail("%s where %s goes (a JSON object)", jsonText(j), t)
		}
		return decodeRecord(obj, r, at, t)
	case "sum":
		return decodeSum(j, r, at, t)
	}
	return fail("the type %s", t)
}

// decodeRecord decodes obj as the record type r (written t).
func decodeRecord(obj map[string]any, r *qtype, at string, t *qtype) (itf.Value, error) {
	out := itf.Value{Kind: itf.Record, Fields: map[string]itf.Value{}}
	used := map[string]bool{}
	for _, f := range r.fields {
		key, ok := f.name, false
		if _, ok = obj[key]; !ok {
			for _, k := range sortedKeys(obj) {
				if normField(k) == normField(f.name) {
					key, ok = k, true
					break
				}
			}
		}
		if !ok {
			return itf.Value{}, fmt.Errorf("at %s: the record has no field %s (it has: %s; %s needs every field)", pathOr(at), f.name, strings.Join(sortedKeys(obj), ", "), t)
		}
		used[key] = true
		v, err := decodeAs(obj[key], f.typ, at+"."+f.name)
		if err != nil {
			return itf.Value{}, err
		}
		out.Fields[f.name] = v
	}
	for _, k := range sortedKeys(obj) {
		if !used[k] {
			return itf.Value{}, fmt.Errorf("at %s: the field %s is none of %s's (record what the model has, or add the field to the model)", pathOr(at), k, t)
		}
	}
	return out, nil
}

// decodeSum decodes j as the sum r (written t).
func decodeSum(j any, r *qtype, at string, t *qtype) (itf.Value, error) {
	tagOf := func(name string) (qfield, bool) {
		for _, f := range r.fields {
			if f.name == name {
				return f, true
			}
		}
		return qfield{}, false
	}
	tags := make([]string, len(r.fields))
	for i, f := range r.fields {
		tags[i] = f.name
	}
	nullary := func(f qfield) bool { p := f.typ.resolved(); return p.kind == "tup" && len(p.fields) == 0 }
	variant := func(tag string, payload any, has bool) (itf.Value, error) {
		f, ok := tagOf(tag)
		if !ok {
			return itf.Value{}, fmt.Errorf("at %s: the tag %q is none of %s's (%s)", pathOr(at), tag, t, strings.Join(tags, ", "))
		}
		if nullary(f) {
			if has && payload != nil {
				return itf.Value{}, fmt.Errorf("at %s: %s carries nothing; the value gives it %s", pathOr(at), tag, jsonText(payload))
			}
			return itf.Enum(tag), nil
		}
		if !has {
			return itf.Value{}, fmt.Errorf("at %s: %s carries %s; the value gives it nothing", pathOr(at), tag, f.typ)
		}
		v, err := decodeAs(payload, f.typ, at+"."+tag)
		if err != nil {
			return itf.Value{}, err
		}
		return itf.VariantOf(tag, v), nil
	}
	switch x := j.(type) {
	case nil:
		if f, ok := tagOf("None"); ok && nullary(f) {
			return itf.Enum("None"), nil
		}
		return itf.Value{}, fmt.Errorf("at %s: null where %s goes, which has no None", pathOr(at), t)
	case string:
		return variant(x, nil, false)
	case map[string]any:
		if tag, ok := x["tag"].(string); ok {
			if p, ok := x["value"]; ok && len(x) == 2 {
				return variant(tag, p, true)
			}
			if len(x) == 1 {
				return variant(tag, nil, false)
			}
			// the payload's fields beside the tag: {"tag": "Created", "counter": ...}
			rest := map[string]any{}
			for k, v := range x {
				if k != "tag" {
					rest[k] = v
				}
			}
			return variant(tag, rest, true)
		}
		if len(x) == 1 {
			for k, v := range x {
				return variant(k, v, true)
			}
		}
	}
	return itf.Value{}, fmt.Errorf("at %s: %s where %s goes (\"Tag\", {\"tag\": \"Tag\", \"value\": v} or {\"Tag\": v})", pathOr(at), jsonText(j), t)
}

func pathOr(at string) string {
	if at == "" {
		return "the value"
	}
	return at
}

func normField(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

// isTime reports whether s is a time as Go writes one: RFC 3339
// (encoding/json's time.Time) or time.Time's String (fmt's).
func isTime(s string) bool {
	if len(s) < 19 || s[4] != '-' {
		return false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02T15:04:05"} {
		if _, err := time.Parse(layout, strings.SplitN(s, " m=", 2)[0]); err == nil {
			return true
		}
	}
	return false
}

func jsonText(j any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(j) != nil {
		return fmt.Sprint(j)
	}
	return strings.TrimSpace(b.String())
}

// qfloat is a recorded float: printed as quintLit prints it (truncated:
// a template that reads it as a scalar is as it was), and refused by
// toQuint, which sees it whole.
type qfloat float64

func (f qfloat) String() string { return quintLit(float64(f)) }
