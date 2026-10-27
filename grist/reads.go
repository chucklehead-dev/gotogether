package grist

import (
	"context"
	"crypto/sha1"
	"fmt"
)

// Reads is a decision's only access to state. Its first read pins the
// position: the head at that read, P. Every later read of the same Reads
// folds through P, never past it, so a decision sees one state whatever
// commits while it runs. Every read is recorded as a query (an event
// type set and a tag set), and the decision's append is conditioned on
// their union: it commits only if no event matching any of them was
// appended after P (docs/grist.md §2.3).
//
// A processor's Reads are pinned at its trigger's position and condition
// nothing: the prefix through the trigger never changes.
type Reads struct {
	ctx     context.Context
	store   Store
	tenant  string
	pos     Position
	pinned  bool
	queries []Query
	id      string // the command id, for ID
	// err is the first store error (transient: the try is retried); bad
	// the first misuse (the code's fault)
	err, bad error
}

func newReads(ctx context.Context, s Store, tenant, id string) *Reads {
	return &Reads{ctx: ctx, store: s, tenant: tenant, id: id}
}

// at is a Reads pinned at p.
func readsAt(ctx context.Context, s Store, tenant string, p Position, id string) *Reads {
	return &Reads{ctx: ctx, store: s, tenant: tenant, id: id, pos: p, pinned: true}
}

// Tenant is the tenant the decision is about.
func (r *Reads) Tenant() string { return r.tenant }

// Position is the position the reads are pinned at, pinning it (at the
// head) if nothing was read yet.
func (r *Reads) Position() Position {
	if !r.pinned {
		r.read(Query{})
	}
	return r.pos
}

// read runs q through the pinned position (pinning it at the head on the
// first read) and adds q to the read set.
func (r *Reads) read(q Query) []Stored {
	if r.err != nil || r.bad != nil {
		return nil
	}
	if len(q.Tags) > 1 {
		r.bad = fmt.Errorf("grist: query %s has %d tags: slice 1 records a read set as (type, tag, value) triples, one tag per query", q, len(q.Tags))
		return nil
	}
	if len(q.Types) > 0 {
		r.addQuery(Query{Types: q.Types, Tags: q.Tags})
	}
	if r.pinned && r.pos == 0 {
		return nil // nothing through position 0 (and 0 would read the head)
	}
	evs, at, err := r.store.Read(r.ctx, r.tenant, q, r.pos)
	if err != nil {
		r.err = err
		return nil
	}
	if !r.pinned {
		r.pos, r.pinned = at, true
	}
	return evs
}

func (r *Reads) addQuery(q Query) {
	for _, have := range r.queries {
		if have.String() == q.String() {
			return
		}
	}
	r.queries = append(r.queries, q)
}

// Exists reports whether an event of type t with every tag of tags
// exists through the pinned position: a query that folds nothing (the
// uniqueness check, Exists(Created, Tag("name", n))).
func (r *Reads) Exists(t EventName, tags ...TagPair) bool {
	return len(r.read(Query{Types: []string{t.Name()}, Tags: tags, Limit: 1})) > 0
}

// Events runs a raw query through the pinned position. A query with
// Reverse or Limit conditions the append on its whole query, not on the
// events it returned: a later event matching it would have changed the
// answer.
func (r *Reads) Events(q Query) []Stored { return r.read(q) }

// ID is the n-th id the decision mints: a UUIDv5 of the command id and n,
// so that a decision stays a function of its inputs and its retries mint
// the same ids.
func (r *Reads) ID(n int) string { return uuid5(r.id + "/" + fmt.Sprint(n)) }

// namespace is grist's UUIDv5 namespace. Read-only.
var namespace = [16]byte{0x6b, 0xa7, 0xb8, 0x14, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}

func uuid5(name string) string {
	h := sha1.New()
	h.Write(namespace[:])
	h.Write([]byte(name))
	s := h.Sum(nil)
	s[6] = s[6]&0x0f | 0x50
	s[8] = s[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", s[0:4], s[4:6], s[6:8], s[8:10], s[10:16])
}

// readModelInfo is a read model's registration, for Verify.
type readModelInfo struct {
	name    string
	version int
	types   []string
	keys    []string
}

// A Handler is one event type's step of a fold (On).
type Handler[S any] struct {
	typ  string
	fold func(S, Stored) (S, error)
}

// On is a fold's handler for events of type t.
func On[S, E any](t *EventType[E], f func(s S, e E, m Meta) S) Handler[S] {
	return Handler[S]{typ: t.Name(), fold: func(s S, st Stored) (S, error) {
		e, err := t.Decode(st)
		if err != nil {
			return s, err
		}
		return f(s, e, st.Meta()), nil
	}}
}

// A ReadModel is a fold over a tenant's events of its handlers' types:
// an initial value and one handler per type. The consumed types are the
// handlers', so they cannot drift from a declaration. Slice 1 folds from
// the log on every read (no cache): the version is the cache's, for
// later.
type ReadModel[S any] struct {
	info *readModelInfo
	init func() S
	on   map[string]func(S, Stored) (S, error)
}

// NewReadModel registers a plain read model: it folds every event of its
// handlers' types.
func NewReadModel[S any](app *App, name string, version int, init func() S, handlers ...Handler[S]) *ReadModel[S] {
	app.register("read model", name)
	m := &ReadModel[S]{info: &readModelInfo{name: name, version: version}, init: init, on: map[string]func(S, Stored) (S, error){}}
	for _, h := range handlers {
		if _, dup := m.on[h.typ]; dup {
			app.problems = append(app.problems, fmt.Sprintf("read model %q has two handlers of %s: one per event type", name, h.typ))
		}
		m.on[h.typ] = h.fold
		m.info.types = append(m.info.types, h.typ)
	}
	app.readModels = append(app.readModels, m.info)
	return m
}

// Name is the read model's name.
func (m *ReadModel[S]) Name() string { return m.info.name }

// Query is the read model's query: its types, the tags given.
func (m *ReadModel[S]) Query(tags ...TagPair) Query {
	return Query{Types: append([]string(nil), m.info.types...), Tags: tags}
}

// Fold folds events (in position order) from the initial value: the
// read model's state over them, for a unit test of the fold.
func (m *ReadModel[S]) Fold(events []Stored) (S, error) {
	s := m.init()
	for _, e := range events {
		if f := m.on[e.Type]; f != nil {
			var err error
			if s, err = f(s, e); err != nil {
				return s, fmt.Errorf("grist: read model %s at event %d (%s): %w", m.info.name, e.Pos, e.Type, err)
			}
		}
	}
	return s, nil
}

// Get is the read model's state through r's position: a query of its
// types, in r's read set.
func (m *ReadModel[S]) Get(r *Reads) S { return m.get(r, nil) }

func (m *ReadModel[S]) get(r *Reads, tags []TagPair) S {
	s, err := m.Fold(r.read(m.Query(tags...)))
	if err != nil && r.bad == nil {
		r.bad = err
	}
	return s
}

// KeyNames are a keyed read model's key tags (Keys).
type KeyNames []string

// Keys names a keyed read model's key tag. Slice 1 takes one: a read set
// is recorded as (type, tag, value) triples (docs/grist.md §8.1).
func Keys(names ...string) KeyNames { return KeyNames(names) }

// A KeyedReadModel folds only the events carrying the given values of
// its key tags: a decision about counter c reads, and is conditioned on,
// counter c's events alone.
type KeyedReadModel[S any] struct {
	ReadModel[S]
	keys KeyNames
}

// NewKeyedReadModel registers a keyed read model, keyed by keys' tags.
func NewKeyedReadModel[S any](app *App, name string, version int, keys KeyNames, init func() S, handlers ...Handler[S]) *KeyedReadModel[S] {
	m := &KeyedReadModel[S]{ReadModel: *NewReadModel(app, name, version, init, handlers...), keys: keys}
	m.info.keys = keys
	if len(keys) != 1 {
		app.problems = append(app.problems, fmt.Sprintf("read model %q is keyed by %d tags: slice 1 keys a read model by one tag (its read set is (type, tag, value) triples)", name, len(keys)))
	}
	return m
}

// For is the read model's state for the given key values (one per key)
// through r's position.
func (m *KeyedReadModel[S]) For(r *Reads, values ...string) S {
	if len(values) != len(m.keys) {
		if r.bad == nil {
			r.bad = fmt.Errorf("grist: read model %s is keyed by %v: For takes %d values, not %d", m.info.name, m.keys, len(m.keys), len(values))
		}
		return m.init()
	}
	tags := make([]TagPair, len(values))
	for i, v := range values {
		tags[i] = TagPair{Key: m.keys[i], Value: v}
	}
	return m.get(r, tags)
}
