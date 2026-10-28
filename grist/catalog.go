package grist

import (
	"encoding/json"
	"reflect"
	"slices"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
)

// A Catalog is an App's registry as data: what Main's catalog command and
// an API's catalog endpoint serve, and what Generate writes the model's
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
	}{c.Name, c.Quint, Schema(c.Request), Schema(c.Response), c.Keyed, c.Refuses, nonNil(c.Users)})
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
	Version int
	Body    reflect.Type
	Tags    []string // the tag keys, in field order
}

// A CatalogReadModel is a registered read model.
type CatalogReadModel struct {
	Name    string   `json:"name"`
	Version int      `json:"version"`
	Types   []string `json:"types"` // the event types it folds
	Keys    []string `json:"keys"`  // a keyed read model's key tags; none for a plain one
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
		Name    string         `json:"name"`
		Version int            `json:"version"`
		Body    map[string]any `json:"body"`
		Tags    []string       `json:"tags"`
	}{e.Name, e.Version, Schema(e.Body), nonNil(e.Tags)})
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

func nonNil(s []string) []string {
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
	// Delivery and Success an effect's delivery and success event type
	// ("" for none).
	Shape    string   `json:"shape,omitempty"`
	Emits    []string `json:"emits,omitempty"`
	Delivery Delivery `json:"delivery,omitempty"`
	Success  string   `json:"success,omitempty"`
	// Uses are the calls its effect makes (Uses), by Quint name.
	Uses []string `json:"uses,omitempty"`
}

// MarshalText writes the delivery by name.
func (d Delivery) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// Catalog is the App's registry.
func (a *App) Catalog() Catalog {
	c := Catalog{Name: a.Name}
	c.Events = append(c.Events, catalogEvent(a.events[failedType]), catalogEvent(a.events[unknownType]))
	for _, n := range a.eventOrder {
		c.Events = append(c.Events, catalogEvent(a.events[n]))
	}
	for _, rm := range a.readModels {
		c.ReadModels = append(c.ReadModels, CatalogReadModel{Name: rm.name, Version: rm.version,
			Types: append([]string(nil), rm.types...), Keys: append([]string(nil), rm.keys...)})
	}
	c.Rules = []CatalogRule{}
	for _, ri := range a.rules {
		c.Rules = append(c.Rules, CatalogRule{Name: ri.name, Quint: ri.quint, On: ri.on.name, Refuses: slices.Clone(ri.opts.refuses), Guards: nonNil(a.guards(ri))})
	}
	for _, n := range a.Commands() {
		ci := a.commands[n]
		cb := CatalogBlock{Name: ci.name, Quint: ci.quint, Args: ci.args, Unconditioned: ci.opts.unconditioned,
			Authorized: ci.opts.authorize != nil, Internal: ci.opts.internal, Refuses: slices.Clone(ci.opts.refuses)}
		if ci.opts.appends != nil {
			cb.Appends = typeNames(ci.opts.appends)
		}
		for _, ri := range a.rules {
			if !slices.Contains(cb.Appends, ri.on.name) {
				continue
			}
			cb.Rules = append(cb.Rules, ri.name)
			for _, r := range ri.opts.refuses {
				if !slices.Contains(cb.RuleRefuses, r) {
					cb.RuleRefuses = append(cb.RuleRefuses, r)
				}
				if !slices.Contains(cb.Refuses, r) {
					cb.Refuses = append(cb.Refuses, r)
				}
			}
		}
		c.Commands = append(c.Commands, cb)
	}
	for _, n := range a.Queries() {
		qi := a.queries[n]
		c.Queries = append(c.Queries, CatalogBlock{Name: qi.name, Quint: qi.quint, Args: qi.args, Result: qi.result,
			Authorized: qi.opts.authorize != nil, Internal: qi.opts.internal})
	}
	for _, p := range a.processors {
		cp := CatalogProcessor{Name: p.name, Quint: p.quint, On: p.on, EffectTries: p.opts.effectTries}
		switch sh := p.opts.shape; sh.kind {
		case emitResult:
			cp.Shape = "emits"
			for _, e := range sh.emits {
				cp.Emits = append(cp.Emits, e.Name())
			}
		case effectResult:
			cp.Shape, cp.Delivery = "effect", sh.delivery
			if sh.success != nil {
				cp.Success = sh.success.Name()
			}
		}
		for _, u := range p.opts.uses {
			cp.Uses = append(cp.Uses, u.quint)
		}
		c.Processors = append(c.Processors, cp)
	}
	for _, s := range a.sites {
		c.Sites = append(c.Sites, CatalogSite{Name: s.name, Quint: s.quint, Fields: s.typ})
	}
	for _, d := range a.depOrder {
		cd := CatalogDependency{Name: d.name, Quint: d.quint, Calls: []CatalogCall{}}
		for _, ci := range d.calls {
			cc := CatalogCall{Name: ci.name, Quint: ci.quint, Request: ci.req, Response: ci.resp, Keyed: ci.opts.keyed, Refuses: slices.Clone(ci.opts.refuses)}
			for _, u := range ci.users {
				cc.Users = append(cc.Users, u.name)
			}
			cd.Calls = append(cd.Calls, cc)
		}
		c.Dependencies = append(c.Dependencies, cd)
	}
	return c
}

func catalogEvent(i *eventInfo) CatalogEvent {
	return CatalogEvent{Name: i.name, Version: i.version, Body: i.typ, Tags: i.tagKeys()}
}
