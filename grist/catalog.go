package grist

import "reflect"

// A Catalog is an App's registry as data: what slice 2's generator
// (grist model) writes a model skeleton and its binding from, and what
// an API's catalog endpoint serves. It is a copy: changing it changes
// nothing registered.
type Catalog struct {
	Name       string
	Events     []CatalogEvent // in registration order, grist.failed first
	ReadModels []CatalogReadModel
	Commands   []CatalogBlock // sorted by name
	Queries    []CatalogBlock // sorted by name
	Processors []CatalogProcessor
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
	Name    string
	Version int
	Types   []string // the event types it folds
	Keys    []string // a keyed read model's key tags; none for a plain one
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
}

// A CatalogProcessor is a registered processor.
type CatalogProcessor struct {
	Name        string
	Quint       string // its model action's name (its effect steps' is Quint+"Effect")
	On          string // the event type it handles
	EffectTries int
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
		c.Commands = append(c.Commands, CatalogBlock{Name: ci.name, Quint: ci.quint, Args: ci.args, Unconditioned: ci.opts.unconditioned})
	}
	for _, n := range a.Queries() {
		qi := a.queries[n]
		c.Queries = append(c.Queries, CatalogBlock{Name: qi.name, Quint: qi.quint, Args: qi.args, Result: qi.result})
	}
	for _, p := range a.processors {
		c.Processors = append(c.Processors, CatalogProcessor{Name: p.name, Quint: p.quint, On: p.on, EffectTries: p.opts.effectTries})
	}
	return c
}

func catalogEvent(i *eventInfo) CatalogEvent {
	return CatalogEvent{Name: i.name, Version: i.version, Body: i.typ, Tags: i.tagKeys()}
}
