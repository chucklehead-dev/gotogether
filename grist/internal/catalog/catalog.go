// Package catalog is a grist App's registry as data (the Catalog, which
// package grist aliases) and the names the generator and the runtime
// share: a block's Quint name, a type's JSON Schema, a call's unknown
// outcome. It imports nothing of grist, so the generator (grist/internal/gen)
// and grist both import it.
package catalog

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
)

// A Catalog is an App's registry as data: what Main's catalog command and
// an API's catalog endpoint serve, and what the generator (grist/internal/gen) writes the model's
// skeleton and its bindings from (Main's model command). It is a copy:
// changing it changes nothing registered. Its JSON (json.Marshal, the catalog endpoint's and
// Main's catalog command's) writes each type as a JSON Schema (Schema).
type Catalog struct {
	Name       string             `json:"name"`
	Events     []CatalogEvent     `json:"events"` // in registration order, grist.failed and grist.unknown first
	ReadModels []CatalogReadModel `json:"readModels"`
	Commands   []CatalogBlock     `json:"commands"` // sorted by name
	Queries    []CatalogBlock     `json:"queries"`  // sorted by name
	Processors []CatalogProcessor `json:"processors"`
	// Rules are the App's rules (NewRule), in registration order: the
	// order they run in on one event.
	Rules []CatalogRule `json:"rules"`
	Sites []CatalogSite `json:"sites"` // in registration order
	// Dependencies are the App's dependencies (NewDependency), in
	// registration order, each with its calls.
	Dependencies []CatalogDependency `json:"dependencies"`
}

// A CatalogDependency is a registered dependency.
type CatalogDependency struct {
	Name  string        `json:"name"`
	Quint string        `json:"quint"`
	Calls []CatalogCall `json:"calls"` // in registration order
}

// A CatalogCall is a declared call (NewCall).
type CatalogCall struct {
	Name  string
	Quint string // its caller's step's action; its site's is Quint+"Served"
	// Request and Response are its types.
	Request, Response reflect.Type
	// Keyed says the dependency applies each key once (Keyed); Refuses are
	// its declared refusals (Refuses), in the order given.
	Keyed   bool
	Refuses []string
	// Users are the processors that declare it (Uses), in registration
	// order.
	Users []string
}

// MarshalJSON writes the call with its request and response as JSON
// Schemas.
func (c CatalogCall) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name     string         `json:"name"`
		Quint    string         `json:"quint"`
		Request  map[string]any `json:"request"`
		Response map[string]any `json:"response"`
		Keyed    bool           `json:"keyed,omitempty"`
		Refuses  []string       `json:"refuses,omitempty"`
		Users    []string       `json:"users"`
	}{c.Name, c.Quint, Schema(c.Request), Schema(c.Response), c.Keyed, c.Refuses, NonNil(c.Users)})
}

// A CatalogSite is a registered site (NewSite): a dependency's record of
// its effects.
type CatalogSite struct {
	Name   string
	Quint  string       // its step's action
	Fields reflect.Type // the record's type
}

// MarshalJSON writes the site with its record as a JSON Schema.
func (s CatalogSite) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name   string         `json:"name"`
		Quint  string         `json:"quint"`
		Fields map[string]any `json:"fields"`
	}{s.Name, s.Quint, Schema(s.Fields)})
}

// A CatalogEvent is a registered event type.
type CatalogEvent struct {
	Name    string
	Version int // the newest: what an append writes
	Body    reflect.Type
	Tags    []string // the tag keys, in field order
	// Older are its older versions, oldest first, each with the upcaster
	// to the next (grist.Upcast): what a stored event of each may be.
	Older []CatalogVersion
	// Writes is the version an append writes when it is an older one
	// (grist.WritesVersion, while a rollout lasts); 0: Version.
	Writes int
}

// A CatalogVersion is an older version of an event type: the struct its
// events were stored as, and the fields its upcaster drops on purpose.
type CatalogVersion struct {
	Version int
	Body    reflect.Type
	Tags    []string
	Drops   []string
}

// MarshalJSON writes the version with its body as a JSON Schema.
func (v CatalogVersion) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Version int            `json:"version"`
		Body    map[string]any `json:"body"`
		Tags    []string       `json:"tags"`
		Drops   []string       `json:"drops,omitempty"`
	}{v.Version, Schema(v.Body), NonNil(v.Tags), v.Drops})
}

// A CatalogReadModel is a registered read model.
type CatalogReadModel struct {
	Name    string   `json:"name"`
	Version int      `json:"version"`
	Types   []string `json:"types"` // the event types it folds
	Keys    []string `json:"keys"`  // a keyed read model's key tags; none for a plain one
	// Fingerprint is what its snapshots are keyed by: its name, version,
	// keys, the shapes of the events it folds, and what its fold makes of
	// generated events (grist's snapshots, docs/grist.md §2.2). It changes
	// when any of them does, so no snapshot of another fold is reused.
	Fingerprint string `json:"fingerprint"`
}

// A CatalogBlock is a registered command or query.
type CatalogBlock struct {
	Name   string
	Quint  string // its model action's name
	Args   reflect.Type
	Result reflect.Type // a query's; nil for a command
	// Unconditioned is a command's stated reason for appending whatever
	// commits while it decides; "" when it is conditioned on its reads.
	Unconditioned string
	// Authorized says the block declares a Policy (Authorize); Internal
	// that no adapter serves it (Internal).
	Authorized, Internal bool
	// Refuses are a command's rejections: those it declares (Refuses), in
	// the order given, then those its rules add (RuleRefuses) that it does
	// not declare; nil when it declares none and no rule guards it.
	Refuses []string
	// Appends are the event types a command declares it appends (Appends),
	// nil when it declares none; Rules are the rules that guard it (each
	// on an event type it appends), in registration order, and
	// RuleRefuses their rejections, in that order.
	Appends, Rules, RuleRefuses []string
}

// A CatalogRule is a registered rule (NewRule).
type CatalogRule struct {
	Name  string `json:"name"`
	Quint string `json:"quint"` // the prefix of its holes (Quint+"Refusal", Quint+"Needs")
	On    string `json:"on"`    // the event type it guards
	// Refuses are its declared rejections (Refuses), in the order given;
	// Guards the commands it guards (those declaring On in Appends),
	// sorted.
	Refuses []string `json:"refuses"`
	Guards  []string `json:"guards"`
}

// MarshalJSON writes the event with its body as a JSON Schema.
func (e CatalogEvent) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name    string           `json:"name"`
		Version int              `json:"version"`
		Body    map[string]any   `json:"body"`
		Tags    []string         `json:"tags"`
		Older   []CatalogVersion `json:"older,omitempty"`
		Writes  int              `json:"writes,omitempty"`
	}{e.Name, e.Version, Schema(e.Body), NonNil(e.Tags), e.Older, e.Writes})
}

// MarshalJSON writes the block with its arguments and result as JSON
// Schemas.
func (b CatalogBlock) MarshalJSON() ([]byte, error) {
	var res map[string]any
	if b.Result != nil {
		res = Schema(b.Result)
	}
	return json.Marshal(struct {
		Name          string         `json:"name"`
		Quint         string         `json:"quint"`
		Args          map[string]any `json:"args"`
		Result        map[string]any `json:"result,omitempty"`
		Unconditioned string         `json:"unconditioned,omitempty"`
		Authorized    bool           `json:"authorized"`
		Internal      bool           `json:"internal,omitempty"`
		Refuses       []string       `json:"refuses,omitempty"`
		Appends       []string       `json:"appends,omitempty"`
		Rules         []string       `json:"rules,omitempty"`
		RuleRefuses   []string       `json:"ruleRefuses,omitempty"`
	}{b.Name, b.Quint, Schema(b.Args), res, b.Unconditioned, b.Authorized, b.Internal, b.Refuses, b.Appends, b.Rules, b.RuleRefuses})
}

// NonNil is s, or an empty list for nil (JSON's [] rather than null).
func NonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Schema is typ's JSON Schema as grist derives it by reflection: an
// object of its exported fields by JSON name (no other allowed, every
// one required but a pointer's), strings, integers, booleans, arrays and
// maps of them; a pointer, a slice and a map also take null (Go's nil,
// as encoding/json writes it); nil for a nil type. A type that contains
// itself is {} where it recurs. The API enforces it (ErrBadArgs: a
// required field missing, or null where its type takes none, is a 400).
func Schema(typ reflect.Type) map[string]any { return schema(typ, map[reflect.Type]bool{}) }

func schema(typ reflect.Type, seen map[reflect.Type]bool) map[string]any {
	if typ == nil {
		return nil
	}
	switch typ.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Pointer:
		return nullable(schema(typ.Elem(), seen))
	case reflect.Slice:
		return map[string]any{"type": []string{"array", "null"}, "items": schema(typ.Elem(), seen)}
	case reflect.Array:
		return map[string]any{"type": "array", "items": schema(typ.Elem(), seen)}
	case reflect.Map:
		return map[string]any{"type": []string{"object", "null"}, "additionalProperties": schema(typ.Elem(), seen)}
	case reflect.Struct:
		if seen[typ] {
			return map[string]any{}
		}
		seen[typ] = true
		defer delete(seen, typ)
		props, required := map[string]any{}, []string{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			n := rec.JSONName(f)
			props[n] = schema(f.Type, seen)
			if f.Type.Kind() != reflect.Pointer {
				required = append(required, n)
			}
		}
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	return map[string]any{}
}

// nullable is s also taking null.
func nullable(s map[string]any) map[string]any {
	switch t := s["type"].(type) {
	case string:
		s["type"] = []string{t, "null"}
	case []string:
		if !slices.Contains(t, "null") {
			s["type"] = append(t, "null")
		}
	}
	return s
}

// A CatalogProcessor is a registered processor.
type CatalogProcessor struct {
	Name        string `json:"name"`
	Quint       string `json:"quint"` // its model action's name (its effect steps' is Quint+"Effect", an at-most-once outcome's Quint+"Outcome", a Delivers processor's skip Quint+"Skip")
	On          string `json:"on"`    // the event type it handles
	EffectTries int    `json:"effectTries"`
	// Shape is its declared result: "emits" (Emits), "effect" (Delivers),
	// or "" when it declares none. Emits are an Emits' event types;
	// Guarantee and Success an effect's guarantee and success event type
	// ("" for none).
	Shape     string    `json:"shape,omitempty"`
	Emits     []string  `json:"emits,omitempty"`
	Guarantee Guarantee `json:"guarantee,omitempty"`
	Success   string    `json:"success,omitempty"`
	// Uses are the calls its effect makes (Uses), by Quint name.
	Uses []string `json:"uses,omitempty"`
}

// A Guarantee is an effect's declared delivery guarantee: at least or
// at most once.
type Guarantee int

const (
	// AtLeastOnce runs the effect, then commits the checkpoint (and the
	// outcome event): a crash between them runs it again, under the same
	// EffectKey. An error is tried EffectTries times.
	AtLeastOnce Guarantee = iota + 1
	// AtMostOnce commits the checkpoint first, then runs the effect once,
	// then commits its outcome event (idempotent, under a marker of the
	// effect key): OnSuccess's after a success, grist.failed after an
	// error that says the effect did not happen (NotApplied), and
	// grist.unknown, carrying the error, after any other (it may have: a
	// payment whose answer was lost). A crash between the checkpoint and
	// the effect loses the effect, one after it the outcome, and no restart
	// runs it again. It is tried once (EffectTries does not apply: a failed
	// try may have taken effect).
	AtMostOnce
)

// String is the guarantee's name (AtLeastOnce, AtMostOnce).
func (d Guarantee) String() string {
	switch d {
	case AtLeastOnce:
		return "AtLeastOnce"
	case AtMostOnce:
		return "AtMostOnce"
	}
	return fmt.Sprintf("Guarantee(%d)", int(d))
}

// MarshalText writes the guarantee by name.
func (d Guarantee) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// quintReserved are names Quint will not take as an action's (its
// keywords and the built-ins a model most often meets): `to` fails to
// parse (QNT101). Read-only.
var quintReserved = map[string]bool{
	"to": true, "and": true, "or": true, "not": true, "all": true, "any": true, "if": true, "else": true, "iff": true,
	"implies": true, "val": true, "def": true, "var": true, "const": true, "action": true, "run": true, "temporal": true,
	"nondet": true, "pure": true, "import": true, "export": true, "module": true, "type": true, "assume": true,
	"init": true, "step": true, "match": true, "true": true, "false": true, "Set": true, "List": true, "Map": true,
	"Rec": true, "Tup": true, "Bool": true, "Int": true, "Nat": true, "exists": true, "forall": true,
}

// QuintName maps a block's name to its Quint action's: kebab case to
// camel case ("create-counter" is createCounter). A name that does not
// map to an identifier, or maps to a reserved one, is an error with the
// fix.
func QuintName(name string) (string, error) {
	parts := strings.Split(name, "-")
	var b strings.Builder
	for i, p := range parts {
		if p == "" {
			return "", fmt.Errorf("empty segment: write names as lower-case words joined by single dashes (create-counter)")
		}
		for j, r := range p {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z') || i == 0 && j == 0 && !unicode.IsLetter(r) {
				return "", fmt.Errorf("%q is no Quint identifier once mapped: letters and digits joined by dashes, a letter first (create-counter)", name)
			}
		}
		if i == 0 {
			b.WriteString(p)
		} else {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	q := b.String()
	if quintReserved[q] {
		return "", fmt.Errorf("maps to %s, which Quint reserves: rename it", q)
	}
	return q, nil
}

// UnknownOutcome is a caller's outcome when the answer does not say
// whether the call happened (a timeout, a lost answer).
const UnknownOutcome = "unknown"

// A Field is a block's scalar field, as a step records it and the model
// takes it.
type Field struct {
	JSON  string // its JSON name: the step's field
	Param string // its Quint parameter's name (JSON, or JSON_ where Quint reserves it)
	Type  string // str, int or bool
	Tag   bool   // tagged `grist:"tag"`
}

// QuintScalar is t's Quint type when t is a scalar a step records: str,
// int (booleans as themselves), or "".
func QuintScalar(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "str"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int"
	}
	return ""
}

// QuintIdent is a Quint identifier (read-only).
var QuintIdent = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// ScalarFields are t's top-level scalar fields, as a step records them
// (rec.StepFields): non-scalar fields are not recorded, and so not
// modelled, and each must say so (`grist:"-"`), or it is refused. A
// pointer field is refused (recorded only when set, so a step may lack
// its parameter), as is a JSON name that is no Quint identifier.
func ScalarFields(t reflect.Type) ([]Field, error) {
	if t == nil || t.Kind() != reflect.Struct {
		return nil, nil
	}
	var out []Field
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		n := rec.JSONName(f)
		if f.Type.Kind() == reflect.Pointer && QuintScalar(f.Type.Elem()) != "" {
			return nil, fmt.Errorf("field %s is a pointer, recorded only when set: make it a value", f.Name)
		}
		q := QuintScalar(f.Type)
		if q == "" {
			// a step records scalars only: a list or a struct dropped
			// without a word once left a query's answer uncompared
			if f.Tag.Get("grist") == "-" {
				continue
			}
			return nil, fmt.Errorf("field %s is a %v, which no step records and so no model compares: give the model scalar fields (a count, a joined string), or tag it `grist:\"-\"` to say the model leaves it out", f.Name, f.Type)
		}
		if !QuintIdent.MatchString(n) {
			return nil, fmt.Errorf("field %s's JSON name %q is no Quint identifier: rename it (letters, digits and _)", f.Name, n)
		}
		p := n
		if QuintTaken[n] {
			p = n + "_"
		}
		out = append(out, Field{JSON: n, Param: p, Type: q, Tag: f.Tag.Get("grist") == "tag"})
	}
	return out, nil
}

// QuintTaken are the names a generated parameter may not take: Quint's
// keywords and built-in operators (a parameter named `of` or `size` does
// not parse: QNT101), and the generated modules' own names. Read-only.
var QuintTaken = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Fields(`module import export from as const var val def pure action run temporal nondet
		type assume if else match all any and or not iff implies true false Bool Int Nat Set Map List Tup Rec
		int str bool
		exists forall in union contains fold intersect exclude subseteq map applyTo filter powerset flatten
		allLists allListsUpTo getOnlyElement chooseSome oneOf isFinite size get put keys mapBy setToMap setOfMaps
		set setBy fields with tuples append concat head tail length nth indices replaceAt slice select foldl foldr
		to always eventually next then expect reps fail assert orKeep mustChange enabled weakFair strongFair leadsTo
		range igt ilt igte ilte iadd isub iuminus imul idiv imod ipow actionAll actionAny field fieldNames item
		assign of eq neq ite cross difference matchVariant variant
		log checkpoints appended effects answered init step args_ result_ reads_ rec_ id caller readPos reads
		result`) {
		m[n] = true
	}
	return m
}()
