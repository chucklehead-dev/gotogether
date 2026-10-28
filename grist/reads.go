package grist

import (
	"context"
	"crypto/sha1"
	"fmt"
	"strings"
)

// Reads is a decision's only access to state. Its first read pins the
// position: the head at that read, P. Every later read of the same Reads
// folds through P, never past it, so a decision sees one state whatever
// commits while it runs. Every read is recorded as a selector (an event
// type set and a tag set), and the decision's append is conditioned on
// their union: it commits only if no event matching any of them was
// appended after P (docs/grist.md §2.3).
//
// Every read is checked against the App: a type it does not register, or
// a tag key the type has no tag of (the Go field's name where the JSON
// name is the key, a field not marked grist:"tag"), would match nothing
// and condition nothing, so it is the decision's error, naming the fix,
// never an empty answer.
//
// A processor's Reads are pinned at its trigger's position and condition
// nothing: the prefix through the trigger never changes.
type Reads struct {
	ctx    context.Context
	app    *App
	store  Store
	tenant string
	pos    Position
	pinned bool
	sels   []Selector
	id     string // the command id, for ID
	caller string // the envelope's caller, for Caller
	// err is the first store error (transient: the try is retried); bad
	// the first misuse (the code's fault)
	err, bad error
}

func newReads(ctx context.Context, app *App, s Store, env Envelope) *Reads {
	return &Reads{ctx: ctx, app: app, store: s, tenant: env.Tenant, id: env.ID, caller: env.Caller}
}

// readsAt is a Reads pinned at p.
func readsAt(ctx context.Context, app *App, s Store, tenant string, p Position, id string) *Reads {
	return &Reads{ctx: ctx, app: app, store: s, tenant: tenant, id: id, pos: p, pinned: true}
}

// Tenant is the tenant the decision is about.
func (r *Reads) Tenant() string { return r.tenant }

// Caller is the caller the command or query runs for (its Envelope's: at
// the API, the request's authenticated caller), "" for a processor's
// handler (no one's) and an unauthenticated call. A decision may refuse
// by it (only a counter's creator retires it: a Rejection, committed and
// recorded as the key's answer, unlike a policy's 403). Every command's
// and query's step records it as "caller", so the model judges such
// a rule; the decision is a function of its reads, arguments and caller.
func (r *Reads) Caller() string { return r.caller }

// Position is the position the reads are pinned at, pinning it (at the
// head) if nothing was read yet.
func (r *Reads) Position() Position {
	if !r.pinned {
		r.read(Selector{})
	}
	return r.pos
}

// misuse notes the decision's first misuse of its reads.
func (r *Reads) misuse(format string, args ...any) {
	if r.bad == nil {
		r.bad = fmt.Errorf("grist: "+format, args...)
	}
}

// check reports whether q names only the App's event types and their tag
// keys, noting the misuse otherwise.
func (r *Reads) check(q Selector) bool {
	if len(q.Tags) > 1 {
		r.misuse("read %s has %d tags: slice 1 records a read set as (type, tag, value) triples, one tag per read", q, len(q.Tags))
		return false
	}
	for _, t := range q.Types {
		info := r.app.events[t]
		if info == nil {
			r.misuse("read of %q, which %s does not register (it has %v): fix the name, or register the event (NewEvent)", t, r.app.Name, r.app.eventNames())
			return false
		}
		for _, tag := range q.Tags {
			if err := checkText(fmt.Sprintf("read of %s tagged %s", t, tag.Key), tag.Value); err != nil {
				r.misuse("%v", strings.TrimPrefix(err.Error(), "grist: "))
				return false
			}
			if !info.hasTag(tag.Key) {
				r.misuse("read of %s tagged %s=%s: %s has no tag %q (its tags: %v); a tag's key is the field's JSON name, and the field is marked grist:\"tag\"", t, tag.Key, tag.Value, t, tag.Key, info.tagKeys())
				return false
			}
		}
	}
	return true
}

// read runs q through the pinned position (pinning it at the head on the
// first read) and adds q to the read set.
func (r *Reads) read(q Selector) []Stored {
	if r.err != nil || r.bad != nil || !r.check(q) {
		return nil
	}
	if len(q.Types) > 0 {
		r.addSelector(Selector{Types: q.Types, Tags: q.Tags})
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

func (r *Reads) addSelector(q Selector) {
	for _, have := range r.sels {
		if have.String() == q.String() {
			return
		}
	}
	r.sels = append(r.sels, q)
}

// Exists reports whether an event of type t with every tag of tags
// exists through the pinned position: a read that folds nothing (the
// uniqueness check, Exists(Created, Tag("name", n))).
func (r *Reads) Exists(t EventName, tags ...TagPair) bool {
	info := t.registered()
	if r.app.events[info.name] != info {
		r.misuse("Exists(%s): an event type of another App: register it on %s", info.name, r.app.Name)
		return false
	}
	return len(r.read(Selector{Types: []string{info.name}, Tags: tags, Limit: 1})) > 0
}

// Select runs a raw selector through the pinned position. A selector with
// Reverse or Limit conditions the append on its whole selection, not on
// the events it returned: a later event matching it would have changed
// the answer.
func (r *Reads) Select(q Selector) []Stored { return r.read(q) }

// Events are the events of type t carrying every tag of tags through r's
// position, decoded, in position order: the typed form of Select.
func Events[E any](r *Reads, t *EventType[E], tags ...TagPair) []E {
	if r.app.events[t.info.name] != t.info {
		r.misuse("Events(%s): an event type of another App: register it on %s", t.info.name, r.app.Name)
		return nil
	}
	out := []E{}
	for _, s := range r.read(Selector{Types: []string{t.info.name}, Tags: tags}) {
		e, err := t.Decode(s)
		if err != nil {
			r.misuse("event %d (%s): %v", s.Pos, s.Type, err)
			return nil
		}
		out = append(out, e)
	}
	return out
}

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
	info *eventInfo
	fold func(S, Stored) (S, error)
}

// On is a fold's handler for events of type t.
func On[S, E any](t *EventType[E], f func(s S, e E, m Meta) S) Handler[S] {
	return Handler[S]{typ: t.Name(), info: t.info, fold: func(s S, st Stored) (S, error) {
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

// selector is the read model's read: its types, the tags given.
func (m *ReadModel[S]) selector(tags ...TagPair) Selector {
	return Selector{Types: append([]string(nil), m.info.types...), Tags: tags}
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
	s, err := m.Fold(r.read(m.selector(tags...)))
	if err != nil {
		r.misuse("%v", err)
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
	// every handler's type must have every key as a tag: a key that is
	// not one matches nothing, and For would fold nothing, silently
	for _, h := range handlers {
		for _, k := range keys {
			if !h.info.hasTag(k) {
				app.problems = append(app.problems, fmt.Sprintf("read model %q is keyed by %q, which %s has no tag of (its tags: %v): mark the field grist:\"tag\", key by its JSON name, or make the read model plain", name, k, h.typ, h.info.tagKeys()))
			}
		}
	}
	return m
}

// For is the read model's state for the given key values (one per key)
// through r's position.
func (m *KeyedReadModel[S]) For(r *Reads, values ...string) S {
	if len(values) != len(m.keys) {
		r.misuse("read model %s is keyed by %v: For takes %d values, not %d", m.info.name, m.keys, len(m.keys), len(values))
		return m.init()
	}
	tags := make([]TagPair, len(values))
	for i, v := range values {
		tags[i] = TagPair{Key: m.keys[i], Value: v}
	}
	return m.get(r, tags)
}
