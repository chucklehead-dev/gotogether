package grist

import (
	"encoding/json"
	"reflect"
	"slices"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
)

// A Catalog is an App's registry as data: what slice 2's generator
// (grist model) writes a model skeleton and its binding from, and what
// an API's catalog endpoint serves. It is a copy: changing it changes
// nothing registered. Its JSON (json.Marshal, the catalog endpoint's and
// Main's catalog command's) writes each type as a JSON Schema (Schema).
type Catalog struct {
	Name       string             `json:"name"`
	Events     []CatalogEvent     `json:"events"` // in registration order, grist.failed first
	ReadModels []CatalogReadModel `json:"readModels"`
	Commands   []CatalogBlock     `json:"commands"` // sorted by name
	Queries    []CatalogBlock     `json:"queries"`  // sorted by name
	Processors []CatalogProcessor `json:"processors"`
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
	}{b.Name, b.Quint, Schema(b.Args), res, b.Unconditioned, b.Authorized, b.Internal})
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
	Quint       string `json:"quint"` // its model action's name (its effect steps' is Quint+"Effect", an at-most-once outcome's Quint+"Outcome")
	On          string `json:"on"`    // the event type it handles
	EffectTries int    `json:"effectTries"`
}

// Catalog is the App's registry.
func (a *App) Catalog() Catalog {
	c := Catalog{Name: a.Name}
	c.Events = append(c.Events, catalogEvent(a.events[failedType]))
	for _, n := range a.eventOrder {
		c.Events = append(c.Events, catalogEvent(a.events[n]))
	}
	for _, rm := range a.readModels {
		c.ReadModels = append(c.ReadModels, CatalogReadModel{Name: rm.name, Version: rm.version,
			Types: append([]string(nil), rm.types...), Keys: append([]string(nil), rm.keys...)})
	}
	for _, n := range a.Commands() {
		ci := a.commands[n]
		c.Commands = append(c.Commands, CatalogBlock{Name: ci.name, Quint: ci.quint, Args: ci.args, Unconditioned: ci.opts.unconditioned,
			Authorized: ci.opts.authorize != nil, Internal: ci.opts.internal})
	}
	for _, n := range a.Queries() {
		qi := a.queries[n]
		c.Queries = append(c.Queries, CatalogBlock{Name: qi.name, Quint: qi.quint, Args: qi.args, Result: qi.result,
			Authorized: qi.opts.authorize != nil, Internal: qi.opts.internal})
	}
	for _, p := range a.processors {
		c.Processors = append(c.Processors, CatalogProcessor{Name: p.name, Quint: p.quint, On: p.on, EffectTries: p.opts.effectTries})
	}
	return c
}

func catalogEvent(i *eventInfo) CatalogEvent {
	return CatalogEvent{Name: i.name, Version: i.version, Body: i.typ, Tags: i.tagKeys()}
}
