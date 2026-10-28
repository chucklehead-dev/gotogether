// Package simstore is a store.Store over a qenv.Store, for simulations:
// the Store's isolation level, its commit faults (store.<name>.commit, a
// commit applied whose answer is lost) and its history checker
// (GOTOGETHER_CHECK=1) apply to every append, and its snapshot faults
// (store.<name>.snapshot: a put torn, its state cut short, or lost) to
// every snapshot.
//
// Keys, per tenant t: t/<t>/head (the head, the tenant's lock),
// t/<t>/e/<position, 20 digits> (the events), t/<t>/i/<type>/<position>
// and t/<t>/g/<type>/<key>=<value>/<position> (the index),
// t/<t>/m/<command id> (markers), t/<t>/c/<processor> (checkpoints),
// t/<t>/s/<seq, 20 digits> and t/<t>/sseq (the step log, a
// store.StepLog), t/<t>/n/<read model>/<key>/<fingerprint> (snapshots,
// a store.SnapshotStore); and tenants/<t>. Segments are query-escaped, so a
// value holding a slash or an equals sign names no other key.
//
// An append takes the head with GetForUpdate (the tenant's lock),
// checks the marker, the checkpoint and the condition (a Scan of each
// selector's index prefix), writes (the batch's step row among the rows)
// and commits; grist records the step from its row (grist.Relay), as on
// every tier. Events are cloned on the way in
// and out, so neither a writer nor a reader shares a slice with the
// store. Under Snapshot and Serializable
// the loser of a race for the head gets ErrConflict from GetForUpdate,
// transient (grist retries it). Cost: qenv.Store's Scan walks every row
// of the store, so an append is O(rows in the store), every tenant's
// included (docs/grist.md §2.10): fine for explorations of hundreds of
// events.
package simstore

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist/internal/poskey"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// Store is a store.Store over a qenv.Store. Its waits are on the
// qenv.Store's own signal (qenv.Store.Changed), notified at every commit
// whoever made it: two Stores over one qenv.Store wake each other's
// waiters.
type Store struct {
	db       *qenv.Store
	snapshot qenv.FaultKind // store.<name>.snapshot
}

// New returns a grist store over db, named after it.
func New(db *qenv.Store) *Store {
	// a torn put keeps the first half of the state (a write a crash cut
	// short, which a real store's atomic row never is but a file's may
	// be); a lost one fails, nothing written
	return &Store{db: db, snapshot: qenv.DeclareFaults("store."+db.Name+".snapshot", "torn", "lost")}
}

// Name implements store.Store: the qenv.Store's name.
func (s *Store) Name() string { return s.db.Name }

func esc(s string) string { return url.QueryEscape(s) }

func headKey(t string) string                    { return "t/" + esc(t) + "/head" }
func eventKey(t string, p store.Position) string { return "t/" + esc(t) + "/e/" + poskey.Of(p) }
func markerKey(t, id string) string              { return "t/" + esc(t) + "/m/" + esc(id) }
func checkpointKey(t, p string) string           { return "t/" + esc(t) + "/c/" + esc(p) }
func stepKey(t string, seq int64) string {
	return "t/" + esc(t) + "/s/" + poskey.Of(store.Position(seq))
}
func stepSeqKey(t string) string { return "t/" + esc(t) + "/sseq" }
func snapshotKey(t, name, key, fp string) string {
	return "t/" + esc(t) + "/n/" + esc(name) + "/" + esc(key) + "/" + esc(fp)
}

// indexPrefix is the index prefix a query of typ and tags scans: the
// tag's index if it has one (a query's other tags are checked on the
// events), else the type's.
func indexPrefix(t, typ string, tags []store.TagPair) string {
	if len(tags) > 0 {
		return "t/" + esc(t) + "/g/" + esc(typ) + "/" + esc(tags[0].Key) + "=" + esc(tags[0].Value) + "/"
	}
	return "t/" + esc(t) + "/i/" + esc(typ) + "/"
}

// createTries is CreateTenant's budget of conflicts (grist's default
// transient budget).
const createTries = 10

// CreateTenant implements store.Store.
func (s *Store) CreateTenant(ctx context.Context, t string) error {
	return qenv.Retry(ctx, s.db, createTries, func(tx qenv.Txn) error {
		if _, ok := tx.Get(headKey(t)); ok {
			return nil
		}
		tx.Put("tenants/"+esc(t), true)
		tx.Put(headKey(t), store.Position(0))
		return nil
	})
}

// Tenants implements store.Store.
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
func head(tx qenv.Txn, t string) (store.Position, bool) {
	v, ok := tx.Get(headKey(t))
	if !ok {
		return 0, false
	}
	return v.(store.Position), true
}

// positions are the positions in (after, at] that the index has under
// the prefix of each of q's types, sorted and distinct.
func positions(tx qenv.Txn, t string, q store.Selector, after, at store.Position) []store.Position {
	seen := map[store.Position]bool{}
	var out []store.Position
	for _, typ := range q.Types {
		prefix := indexPrefix(t, typ, q.Tags)
		for _, kv := range tx.Scan(prefix) {
			p, err := poskey.Parse(strings.TrimPrefix(kv.Key, prefix))
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

// Read implements store.Store.
func (s *Store) Read(ctx context.Context, t string, q store.Selector, at store.Position) ([]store.Stored, store.Position, error) {
	tx := s.db.Begin(ctx)
	defer tx.Rollback()
	h, ok := head(tx, t)
	if !ok {
		return nil, 0, store.ErrUnknownTenant
	}
	if at == 0 || at > h {
		at = h
	}
	out := []store.Stored{}
	if len(q.Types) == 0 {
		return out, at, nil
	}
	for _, p := range positions(tx, t, q, q.After, at) {
		v, ok := tx.Get(eventKey(t, p))
		if !ok {
			continue // an index row whose event a transaction at this level does not see: not committed for it
		}
		if e := v.(store.Stored); q.Matches(e.Event) {
			out = append(out, e.Clone()) // the reader's own: a fold mutating it corrupts nothing
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

// Append implements store.Store.
func (s *Store) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	tx := s.db.Begin(ctx)
	defer tx.Rollback()
	v, ok, err := tx.GetForUpdate(headKey(t)) // the tenant's lock
	if err != nil {
		return 0, store.NotCommitted(err) // a lost race for the lock: nothing committed
	}
	if !ok {
		return 0, store.ErrUnknownTenant
	}
	h := v.(store.Position)
	if b.Marker != nil {
		if _, ok := tx.Get(markerKey(t, b.Marker.ID)); ok {
			return 0, &store.ConditionFailed{Marker: b.Marker.ID}
		}
	}
	if c := b.Checkpoint; c != nil && c.To > h {
		return 0, &store.ConditionFailed{Event: c.To, Past: true, Head: h}
	}
	if c := b.Checkpoint; c != nil {
		v, _, err := tx.GetForUpdate(checkpointKey(t, c.Processor))
		if err != nil {
			return 0, store.NotCommitted(err)
		}
		cur, _ := v.(store.Position)
		if cur != c.From {
			return 0, &store.CheckpointMoved{Processor: c.Processor, Expected: c.From, Found: cur}
		}
	}
	if c := b.Condition; c != nil {
		if c.After > h {
			return 0, &store.ConditionFailed{Event: c.After, Past: true, Head: h}
		}
		for _, q := range c.Selectors {
			for _, p := range positions(tx, t, q, c.After, h) {
				if v, ok := tx.Get(eventKey(t, p)); ok && q.Matches(v.(store.Stored).Event) {
					return 0, &store.ConditionFailed{Selector: q, Event: p}
				}
			}
		}
	}
	for _, e := range b.Events {
		h++
		e = e.Clone() // the store's own: the caller may reuse its slices
		tx.Put(eventKey(t, h), store.Stored{Event: e, Tenant: t, Pos: h, Time: b.Time, Cause: b.Cause})
		tx.Put(indexPrefix(t, e.Type, nil)+poskey.Of(h), true)
		for _, tag := range e.Tags {
			tx.Put(indexPrefix(t, e.Type, []store.TagPair{tag})+poskey.Of(h), true)
		}
	}
	tx.Put(headKey(t), h) // written by every append, so a concurrent one conflicts at every level
	if b.Marker != nil {
		m := *b.Marker
		m.Receipt = store.NewReceipt(t, h, len(b.Events))
		tx.Put(markerKey(t, m.ID), m)
	}
	if c := b.Checkpoint; c != nil {
		tx.Put(checkpointKey(t, c.Processor), c.To)
	}
	if b.Step != nil {
		// under the head's lock, so the sequence is the commit order
		v, _ := tx.Get(stepSeqKey(t))
		seq, _ := v.(int64)
		seq++
		tx.Put(stepSeqKey(t), seq)
		tx.Put(stepKey(t, seq), store.StepRow{Seq: seq, Head: h, Time: store.StepTime(ctx), Step: append([]byte(nil), b.Step...)})
	}
	if err := tx.Commit(); err != nil {
		if errors.Is(err, qenv.ErrConflict) {
			return 0, store.NotCommitted(err) // validation refused it (a deadlock too): nothing applied
		}
		return 0, err // ErrUnknownOutcome: applied, its answer lost
	}
	return h, nil
}

// Marker implements store.Store.
func (s *Store) Marker(ctx context.Context, t, id string) (store.Marker, bool, error) {
	tx := s.db.Begin(ctx)
	defer tx.Rollback()
	if _, ok := head(tx, t); !ok {
		return store.Marker{}, false, store.ErrUnknownTenant
	}
	v, ok := tx.Get(markerKey(t, id))
	if !ok {
		return store.Marker{}, false, nil
	}
	m := v.(store.Marker)
	m.Receipt.Events = append([]store.EventRef{}, m.Receipt.Events...)
	return m, true, nil
}

// Steps implements store.StepLog.
func (s *Store) Steps(ctx context.Context, t string, after int64, limit int) ([]store.StepRow, error) {
	tx := s.db.Begin(ctx)
	defer tx.Rollback()
	if _, ok := head(tx, t); !ok {
		return nil, store.ErrUnknownTenant
	}
	out := []store.StepRow{}
	prefix := "t/" + esc(t) + "/s/"
	for _, kv := range tx.Scan(prefix) {
		if r := kv.Val.(store.StepRow); r.Seq > after {
			r.Step = append([]byte(nil), r.Step...)
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Checkpoint implements store.Store.
func (s *Store) Checkpoint(ctx context.Context, t, processor string) (store.Position, error) {
	tx := s.db.Begin(ctx)
	defer tx.Rollback()
	if _, ok := head(tx, t); !ok {
		return 0, store.ErrUnknownTenant
	}
	v, _ := tx.Get(checkpointKey(t, processor))
	p, _ := v.(store.Position)
	return p, nil
}

// Heads implements store.Store.
func (s *Store) Heads(ctx context.Context) (map[string]store.Position, error) {
	ts, err := s.Tenants(ctx)
	if err != nil {
		return nil, err
	}
	tx := s.db.Begin(ctx)
	defer tx.Rollback()
	out := map[string]store.Position{}
	for _, t := range ts {
		out[t], _ = head(tx, t)
	}
	return out, nil
}

// Wait implements store.Store: it waits on the store's signal, notified
// at every append's commit (and at a failover or a restore of the
// qenv.Store, which may rewind the head), so a poller costs the scheduler
// nothing while the tenant is quiet.
func (s *Store) Wait(ctx context.Context, t string, p store.Position) error {
	return s.wait(ctx, headKey(t), true, func(v any) bool { return v.(store.Position) != p })
}

// StepCount implements store.StepLog.
func (s *Store) StepCount(ctx context.Context, t string) (int64, error) {
	tx := s.db.Begin(ctx)
	defer tx.Rollback()
	if _, ok := head(tx, t); !ok {
		return 0, store.ErrUnknownTenant
	}
	v, _ := tx.Get(stepSeqKey(t))
	n, _ := v.(int64)
	return n, nil
}

// WaitTenants implements store.TenantFeed, as Wait does on the tenants'
// keys.
func (s *Store) WaitTenants(ctx context.Context, n int) error {
	for len(s.db.Keys("tenants/")) <= n {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.db.Changed().Wait(ctx, 0)
	}
	return nil
}

// WaitSteps implements store.StepLog, as Wait does on the step log's
// sequence.
func (s *Store) WaitSteps(ctx context.Context, t string, after int64) error {
	if _, ok := s.db.Read(headKey(t)); !ok {
		return store.ErrUnknownTenant
	}
	return s.wait(ctx, stepSeqKey(t), false, func(v any) bool { n, _ := v.(int64); return n != after })
}

// wait waits on the signal until done holds of key's committed value
// (nil if absent: a tenant with no step yet; the unknown tenant's error
// if must).
func (s *Store) wait(ctx context.Context, key string, must bool, done func(any) bool) error {
	for {
		v, ok := s.db.Read(key)
		if !ok && must {
			return store.ErrUnknownTenant
		}
		if done(v) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.db.Changed().Wait(ctx, 0) // every commit's, this Store's or another's over the same qenv.Store
	}
}

// snapshotTries is PutSnapshot's budget of conflicts: a snapshot is a
// cache, so a put that loses its races is dropped.
const snapshotTries = 3

// PutSnapshot implements store.SnapshotStore, with the store's snapshot
// faults in a simulation.
func (s *Store) PutSnapshot(ctx context.Context, t string, snap store.Snapshot) error {
	snap = snap.Clone()
	if sim := qenv.From(ctx); sim != nil {
		switch sim.FaultOf(s.snapshot) {
		case "torn":
			snap.State = snap.State[:len(snap.State)/2]
		case "lost":
			return errors.New("simstore: snapshot lost (fault store." + s.db.Name + ".snapshot)")
		}
	}
	return qenv.Retry(ctx, s.db, snapshotTries, func(tx qenv.Txn) error {
		if _, ok := head(tx, t); !ok {
			return store.ErrUnknownTenant
		}
		tx.Put(snapshotKey(t, snap.Name, snap.Key, snap.Fingerprint), snap)
		return nil
	})
}

// GetSnapshot implements store.SnapshotStore.
func (s *Store) GetSnapshot(ctx context.Context, t, name, key, fingerprint string) (store.Snapshot, bool, error) {
	tx := s.db.Begin(ctx)
	defer tx.Rollback()
	if _, ok := head(tx, t); !ok {
		return store.Snapshot{}, false, store.ErrUnknownTenant
	}
	v, ok := tx.Get(snapshotKey(t, name, key, fingerprint))
	if !ok {
		return store.Snapshot{}, false, nil
	}
	return v.(store.Snapshot).Clone(), true, nil
}
