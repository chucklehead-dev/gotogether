package grist

import (
	"slices"

	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"
)

// A Catalog is an App's registry as data: what Main's catalog command and
// an API's catalog endpoint serve, and what Main's model command writes
// the model's skeleton and its bindings from. It is a copy: changing it
// changes nothing registered. Its JSON (json.Marshal, the catalog
// endpoint's and Main's catalog command's) writes each type as a JSON
// Schema, as the API enforces it.
type Catalog = catalog.Catalog

// A CatalogDependency is a registered dependency.
type CatalogDependency = catalog.CatalogDependency

// A CatalogCall is a declared call (NewCall).
type CatalogCall = catalog.CatalogCall

// A CatalogSite is a registered site (NewSite): a dependency's record of
// its effects.
type CatalogSite = catalog.CatalogSite

// A CatalogEvent is a registered event type.
type CatalogEvent = catalog.CatalogEvent

// A CatalogVersion is an older version of an event type (Upcast).
type CatalogVersion = catalog.CatalogVersion

// A CatalogReadModel is a registered read model.
type CatalogReadModel = catalog.CatalogReadModel

// A CatalogBlock is a registered command or query.
type CatalogBlock = catalog.CatalogBlock

// A CatalogRule is a registered rule (NewRule).
type CatalogRule = catalog.CatalogRule

// A CatalogProcessor is a registered processor.
type CatalogProcessor = catalog.CatalogProcessor

func nonNil(s []string) []string { return catalog.NonNil(s) }

// Catalog is the App's registry.
func (a *App) Catalog() Catalog {
	c := Catalog{Name: a.Name}
	c.Events = append(c.Events, catalogEvent(a.events[failedType]), catalogEvent(a.events[unknownType]))
	for _, n := range a.eventOrder {
		c.Events = append(c.Events, catalogEvent(a.events[n]))
	}
	for _, rm := range a.readModels {
		c.ReadModels = append(c.ReadModels, CatalogReadModel{Name: rm.name, Version: rm.version,
			Types: append([]string(nil), rm.types...), Keys: append([]string(nil), rm.keys...), Fingerprint: rm.fingerprint()})
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
			cp.Shape, cp.Guarantee = "effect", sh.guarantee
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
	e := CatalogEvent{Name: i.name, Version: i.version, Body: i.typ, Tags: i.tagKeys()}
	for _, u := range i.ups {
		var tags []string
		for _, t := range u.inTags {
			tags = append(tags, t.key)
		}
		e.Older = append(e.Older, CatalogVersion{Version: u.from, Body: u.in, Tags: nonNil(tags), Drops: slices.Clone(u.drops)})
	}
	return e
}
