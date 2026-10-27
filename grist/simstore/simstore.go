// Package simstore is a grist.Store over a qenv.Store, for simulations:
// the Store's isolation level, its commit faults (store.<name>.commit, a
// commit applied whose answer is lost) and its history checker
// (GOTOGETHER_CHECK=1) apply to every append.
//
// Keys, per tenant t: t/<t>/head (the head, the tenant's lock),
// t/<t>/e/<position, 20 digits> (the events), t/<t>/i/<type>/<position>
// and t/<t>/g/<type>/<key>=<value>/<position> (the index),
// t/<t>/m/<command id> (markers), t/<t>/c/<processor> (checkpoints); and
// tenants/<t>. Segments are query-escaped, so a value holding a slash
// or an equals sign names no other key.
//
// An append takes the head with GetForUpdate (the tenant's lock),
// checks the condition with a Scan of each query's index prefix, writes
// and commits; the step is recorded in Txn.OnCommit, whose hooks run
// before ErrUnknownOutcome is returned. Under Snapshot and Serializable
// the loser of a race for the head gets ErrConflict from GetForUpdate,
// transient (grist retries it). Cost: qenv.Store's Scan walks every row
// of the store, so an append is O(rows in the store), every tenant's
// included (docs/grist.md §2.10): fine for explorations of hundreds of
// events.
package simstore

import (
	"context"
	"net/url"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// Store is a grist.Store over a qenv.Store.
type Store struct {
	db      *qenv.Store
	changed qenv.Signal // notified at every append's commit
}

// New returns a grist store over db, named after it.
func New(db *qenv.Store) *Store { return &Store{db: db} }

// Name implements grist.Store: the qenv.Store's name.
func (s *Store) Name() string { return s.db.Name }

func esc(s string) string { return url.QueryEscape(s) }

func headKey(t string) string                    { return "t/" + esc(t) + "/head" }
func eventKey(t string, p grist.Position) string { return "t/" + esc(t) + "/e/" + grist.PosKey(p) }
func markerKey(t, id string) string              { return "t/" + esc(t) + "/m/" + esc(id) }
func checkpointKey(t, p string) string           { return "t/" + esc(t) + "/c/" + esc(p) }

// indexPrefix is the index prefix a query of typ and tags scans: the
// tag's index if it has one (a query's other tags are checked on the
// events), else the type's.
func indexPrefix(t, typ string, tags []grist.TagPair) string {
	if len(tags) > 0 {
		return "t/" + esc(t) + "/g/" + esc(typ) + "/" + esc(tags[0].Key) + "=" + esc(tags[0].Value) + "/"
	}
	return "t/" + esc(t) + "/i/" + esc(typ) + "/"
}

// CreateTenant implements grist.Store.
func (s *Store) CreateTenant(ctx context.Context, t string) error {
	return qenv.Retry(ctx, s.db, grist.TransientTries, func(tx qenv.Txn) error {
		if _, ok := tx.Get(headKey(t)); ok {
			return nil
		}
		tx.Put("tenants/"+esc(t), true)
		tx.Put(headKey(t), grist.Position(0))
		return nil
	})
}

// Tenants implements grist.Store.
func (s *Store) Tenants(ctx context.Context) ([]string, error) {
	tx := s.db.Begin(ctx)
	defer tx.Rollback()
	var out []string
	for _, kv := range tx.Scan("tenants/") {
		t, err := url.QueryUnescape(strings.TrimPrefix(kv.Key, "tenants/"))
		if err == nil {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out, nil
}

// head is t's head as tx reads it.
func head(tx qenv.Txn, t string) (grist.Position, bool) {
	v, ok := tx.Get(headKey(t))
	if !ok {
		return 0, false
	}
	return v.(grist.Position), true
}

// positions are the positions in (after, at] that the index has under
// the prefix of each of q's types, sorted and distinct.
func positions(tx qenv.Txn, t string, q grist.Query, after, at grist.Position) []grist.Position {
	seen := map[grist.Position]bool{}
	var out []grist.Position
	for _, typ := range q.Types {
		prefix := indexPrefix(t, typ, q.Tags)
		for _, kv := range tx.Scan(prefix) {
			p, err := grist.ParsePosKey(strings.TrimPrefix(kv.Key, prefix))
			if err != nil || p <= after || p > at || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Read implements grist.Store.
func (s *Store) Read(ctx context.Context, t string, q grist.Query, at grist.Position) ([]grist.Stored, grist.Position, error) {
	tx := s.db.Begin(ctx)
	defer tx.Rollback()
	h, ok := head(tx, t)
	if !ok {
		return nil, 0, grist.ErrUnknownTenant
	}
	if at == 0 || at > h {
		at = h
	}
	out := []grist.Stored{}
	if len(q.Types) == 0 {
		return out, at, nil
	}
	for _, p := range positions(tx, t, q, q.After, at) {
		v, ok := tx.Get(eventKey(t, p))
		if !ok {
			continue // an index row whose event a transaction at this level does not see: not committed for it
		}
		if e := v.(grist.Stored); q.Matches(e.Event) {
			out = append(out, e)
		}
	}
	if q.Reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, at, nil
}

// Append implements grist.Store.
func (s *Store) Append(ctx context.Context, t string, b grist.Batch) (grist.Position, error) {
	tx := s.db.Begin(ctx)
	defer tx.Rollback()
	v, ok, err := tx.GetForUpdate(headKey(t)) // the tenant's lock
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, grist.ErrUnknownTenant
	}
	h := v.(grist.Position)
	if b.Marker != nil {
		if _, ok := tx.Get(markerKey(t, b.Marker.ID)); ok {
			return 0, &grist.ConditionFailed{Marker: b.Marker.ID}
		}
	}
	if c := b.Checkpoint; c != nil {
		v, _, err := tx.GetForUpdate(checkpointKey(t, c.Processor))
		if err != nil {
			return 0, err
		}
		cur, _ := v.(grist.Position)
		if cur != c.From {
			return 0, &grist.CheckpointMoved{Processor: c.Processor, Expected: c.From, Found: cur}
		}
	}
	if c := b.Condition; c != nil {
		for _, q := range c.Queries {
			for _, p := range positions(tx, t, q, c.After, h) {
				if v, ok := tx.Get(eventKey(t, p)); ok && q.Matches(v.(grist.Stored).Event) {
					return 0, &grist.ConditionFailed{Query: q, Event: p}
				}
			}
		}
	}
	for _, e := range b.Events {
		h++
		e.Tags = append([]grist.TagPair(nil), e.Tags...)
		tx.Put(eventKey(t, h), grist.Stored{Event: e, Tenant: t, Pos: h, Time: b.Time, Cause: b.Cause})
		tx.Put(indexPrefix(t, e.Type, nil)+grist.PosKey(h), true)
		for _, tag := range e.Tags {
			tx.Put(indexPrefix(t, e.Type, []grist.TagPair{tag})+grist.PosKey(h), true)
		}
	}
	tx.Put(headKey(t), h) // written by every append, so a concurrent one conflicts at every level
	if b.Marker != nil {
		m := *b.Marker
		m.Receipt = grist.NewReceipt(t, h, len(b.Events))
		tx.Put(markerKey(t, m.ID), m)
	}
	if c := b.Checkpoint; c != nil {
		tx.Put(checkpointKey(t, c.Processor), c.To)
	}
	tx.OnCommit(func() {
		s.changed.Notify()
		if b.OnCommit != nil {
			b.OnCommit(h)
		}
	})
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return h, nil
}

// Marker implements grist.Store.
func (s *Store) Marker(ctx context.Context, t, id string) (grist.Marker, bool, error) {
	tx := s.db.Begin(ctx)
	defer tx.Rollback()
	if _, ok := head(tx, t); !ok {
		return grist.Marker{}, false, grist.ErrUnknownTenant
	}
	v, ok := tx.Get(markerKey(t, id))
	if !ok {
		return grist.Marker{}, false, nil
	}
	return v.(grist.Marker), true, nil
}

// Checkpoint implements grist.Store.
func (s *Store) Checkpoint(ctx context.Context, t, processor string) (grist.Position, error) {
	tx := s.db.Begin(ctx)
	defer tx.Rollback()
	if _, ok := head(tx, t); !ok {
		return 0, grist.ErrUnknownTenant
	}
	v, _ := tx.Get(checkpointKey(t, processor))
	p, _ := v.(grist.Position)
	return p, nil
}

// Heads implements grist.Store.
func (s *Store) Heads(ctx context.Context) (map[string]grist.Position, error) {
	ts, err := s.Tenants(ctx)
	if err != nil {
		return nil, err
	}
	tx := s.db.Begin(ctx)
	defer tx.Rollback()
	out := map[string]grist.Position{}
	for _, t := range ts {
		out[t], _ = head(tx, t)
	}
	return out, nil
}

// Wait implements grist.Store: it waits on the store's signal, notified
// at every append's commit, so a poller costs the scheduler nothing
// while the tenant is quiet.
func (s *Store) Wait(ctx context.Context, t string, p grist.Position) error {
	for {
		v, ok := s.db.Read(headKey(t))
		if !ok {
			return grist.ErrUnknownTenant
		}
		if v.(grist.Position) > p {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.changed.Wait(ctx, 0)
	}
}
