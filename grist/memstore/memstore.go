// Package memstore is a grist store in memory: maps and a mutex, for unit
// tests of a fold or a decision. It is not for simulations (its mutex is
// a real one, which qenv cannot schedule): use grist/simstore there. It
// keeps a store.StepLog and snapshots (store.SnapshotStore), and is a
// store.TenantFeed.
package memstore

import (
	"context"
	"maps"
	"sort"
	"sync"

	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// Store is an in-memory store.Store.
type Store struct {
	name string

	mu      sync.Mutex
	tenants map[string]*tenant
	ids     map[string]bool // committed event identities across every tenant
	changed chan struct{}   // closed and replaced on every append
}

type tenant struct {
	log         []store.Stored
	markers     map[string]store.Marker
	checkpoints map[string]store.Position
	steps       []store.StepRow
	snapshots   map[[3]string]store.Snapshot // by name, key, fingerprint
}

// New returns an empty store named name.
func New(name string) *Store {
	return &Store{name: name, tenants: map[string]*tenant{}, ids: map[string]bool{}, changed: make(chan struct{})}
}

// Name implements store.Store.
func (s *Store) Name() string { return s.name }

// CreateTenant implements store.Store.
func (s *Store) CreateTenant(_ context.Context, t string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tenants[t] == nil {
		s.tenants[t] = &tenant{markers: map[string]store.Marker{}, checkpoints: map[string]store.Position{}, snapshots: map[[3]string]store.Snapshot{}}
		s.wake()
	}
	return nil
}

// wake wakes every waiter (with s.mu held).
func (s *Store) wake() {
	close(s.changed)
	s.changed = make(chan struct{})
}

// Tenants implements store.Store.
func (s *Store) Tenants(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.tenants))
	for t := range s.tenants {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

// Read implements store.Store: the reader's own copies (store.Select's).
func (s *Store) Read(_ context.Context, t string, q store.Selector, at store.Position) ([]store.Stored, store.Position, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn := s.tenants[t]
	if tn == nil {
		return nil, 0, store.ErrUnknownTenant
	}
	head := store.Position(len(tn.log))
	if at == 0 || at > head {
		at = head
	}
	return store.Select(tn.log, q, at), at, nil
}

// Append implements store.Store. The marker, the checkpoint and the
// condition are checked under the store's lock.
func (s *Store) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	if err := b.CheckRetainedIDs(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	tn := s.tenants[t]
	if tn == nil {
		s.mu.Unlock()
		return 0, store.ErrUnknownTenant
	}
	if b.Marker != nil {
		if _, ok := tn.markers[b.Marker.ID]; ok {
			s.mu.Unlock()
			return 0, &store.ConditionFailed{Marker: b.Marker.ID}
		}
	}
	if c := b.Checkpoint; c != nil && c.To > store.Position(len(tn.log)) {
		err := &store.ConditionFailed{Event: c.To, Past: true, Head: store.Position(len(tn.log))}
		s.mu.Unlock()
		return 0, err
	}
	if c := b.Checkpoint; c != nil && tn.checkpoints[c.Processor] != c.From {
		err := &store.CheckpointMoved{Processor: c.Processor, Expected: c.From, Found: tn.checkpoints[c.Processor]}
		s.mu.Unlock()
		return 0, err
	}
	if b.Condition != nil {
		if err := store.CheckCondition(tn.log, *b.Condition); err != nil {
			s.mu.Unlock()
			return 0, err
		}
	}
	ids := make([]string, len(b.Events))
	allocated := map[string]bool{}
	for i := range ids {
		for {
			id := ""
			if b.RetainedIDs != nil {
				id = b.RetainedIDs[i]
			} else {
				id = qenv.NewID(ctx)
			}
			if s.ids[id] || allocated[id] {
				if b.RetainedIDs != nil {
					s.mu.Unlock()
					return 0, store.Permanent(&store.IdentityExists{ID: id})
				}
				continue // a simulation may import IDs from an earlier run
			}
			ids[i], allocated[id] = id, true
			break
		}
	}
	for i, e := range b.Events {
		tn.log = append(tn.log, store.Stored{Event: e.Clone(), ID: ids[i], Tenant: t, Pos: store.Position(len(tn.log) + 1), Time: b.Time, Cause: b.Cause})
		s.ids[ids[i]] = true
	}
	head := store.Position(len(tn.log))
	if b.Marker != nil {
		m := *b.Marker
		m.Receipt = store.NewReceipt(t, head, len(b.Events))
		tn.markers[m.ID] = m
	}
	if c := b.Checkpoint; c != nil {
		tn.checkpoints[c.Processor] = c.To
	}
	if b.Step != nil {
		tn.steps = append(tn.steps, store.StepRow{Seq: int64(len(tn.steps) + 1), Head: head, Time: store.StepTime(ctx), Step: append([]byte(nil), b.Step...)})
	}
	s.wake()
	s.mu.Unlock()
	return head, nil
}

// Steps implements store.StepLog.
func (s *Store) Steps(_ context.Context, t string, after int64, limit int) ([]store.StepRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn := s.tenants[t]
	if tn == nil {
		return nil, store.ErrUnknownTenant
	}
	out := []store.StepRow{}
	for _, r := range tn.steps {
		if r.Seq > after && (limit == 0 || len(out) < limit) {
			r.Step = append([]byte(nil), r.Step...)
			out = append(out, r)
		}
	}
	return out, nil
}

// Marker implements store.Store.
func (s *Store) Marker(_ context.Context, t, id string) (store.Marker, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn := s.tenants[t]
	if tn == nil {
		return store.Marker{}, false, store.ErrUnknownTenant
	}
	m, ok := tn.markers[id]
	m.Receipt.Events = append([]store.EventRef{}, m.Receipt.Events...)
	return m, ok, nil
}

// Checkpoint implements store.Store.
func (s *Store) Checkpoint(_ context.Context, t, processor string) (store.Position, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn := s.tenants[t]
	if tn == nil {
		return 0, store.ErrUnknownTenant
	}
	return tn.checkpoints[processor], nil
}

// Heads implements store.Store.
func (s *Store) Heads(context.Context) (map[string]store.Position, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]store.Position{}
	for t, tn := range s.tenants {
		out[t] = store.Position(len(tn.log))
	}
	return out, nil
}

// Wait implements store.Store.
func (s *Store) Wait(ctx context.Context, t string, p store.Position) error {
	return s.wait(ctx, t, func(tn *tenant) bool { return store.Position(len(tn.log)) != p })
}

// WaitSteps implements store.StepLog.
func (s *Store) WaitSteps(ctx context.Context, t string, after int64) error {
	return s.wait(ctx, t, func(tn *tenant) bool { return int64(len(tn.steps)) != after })
}

// StepCount implements store.StepLog.
func (s *Store) StepCount(_ context.Context, t string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn := s.tenants[t]
	if tn == nil {
		return 0, store.ErrUnknownTenant
	}
	return int64(len(tn.steps)), nil
}

// WaitTenants implements store.TenantFeed.
func (s *Store) WaitTenants(ctx context.Context, n int) error {
	return s.wait(ctx, "", func(*tenant) bool { return len(s.tenants) > n })
}

// wait waits until done holds of t (of the store, t ""), woken by every
// append and every tenant created.
func (s *Store) wait(ctx context.Context, t string, done func(*tenant) bool) error {
	for {
		s.mu.Lock()
		tn := s.tenants[t]
		if tn == nil && t != "" {
			s.mu.Unlock()
			return store.ErrUnknownTenant
		}
		if done(tn) {
			s.mu.Unlock()
			return nil
		}
		ch := s.changed
		s.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// A Backup is a memstore's whole state at a point (Store.Backup), for a
// test of what a restore does to a node: Store.Restore puts it back.
type Backup struct {
	tenants map[string]*tenant
	ids     map[string]bool
}

// Backup is a copy of the store's state now.
func (s *Store) Backup() *Backup {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &Backup{tenants: copyTenants(s.tenants), ids: maps.Clone(s.ids)}
}

// Restore puts the store back to b, as a restore from a backup does to a
// database (every commit after it gone), and wakes every waiter: a node
// waiting on the head it read sees it go backwards (grist.ErrRewound).
func (s *Store) Restore(b *Backup) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenants = copyTenants(b.tenants)
	s.ids = maps.Clone(b.ids)
	s.wake()
}

// copyTenants is a copy of ts that shares nothing a later append changes.
func copyTenants(ts map[string]*tenant) map[string]*tenant {
	out := make(map[string]*tenant, len(ts))
	for name, tn := range ts {
		c := &tenant{log: append([]store.Stored(nil), tn.log...), steps: append([]store.StepRow(nil), tn.steps...),
			markers: map[string]store.Marker{}, checkpoints: map[string]store.Position{}, snapshots: map[[3]string]store.Snapshot{}}
		for k, v := range tn.markers {
			c.markers[k] = v
		}
		for k, v := range tn.checkpoints {
			c.checkpoints[k] = v
		}
		for k, v := range tn.snapshots {
			c.snapshots[k] = v
		}
		out[name] = c
	}
	return out
}

// PutSnapshot implements store.SnapshotStore.
func (s *Store) PutSnapshot(_ context.Context, t string, snap store.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn := s.tenants[t]
	if tn == nil {
		return store.ErrUnknownTenant
	}
	tn.snapshots[[3]string{snap.Name, snap.Key, snap.Fingerprint}] = snap.Clone()
	return nil
}

// PruneSnapshots implements store.SnapshotPruner.
func (s *Store) PruneSnapshots(_ context.Context, t, name, key, keep string, before store.Position) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn := s.tenants[t]
	if tn == nil {
		return 0, store.ErrUnknownTenant
	}
	n := 0
	for k, snap := range tn.snapshots {
		if k[0] == name && k[1] == key && k[2] != keep && snap.Pos < before {
			delete(tn.snapshots, k)
			n++
		}
	}
	return n, nil
}

// GetSnapshot implements store.SnapshotStore.
func (s *Store) GetSnapshot(_ context.Context, t, name, key, fingerprint string) (store.Snapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn := s.tenants[t]
	if tn == nil {
		return store.Snapshot{}, false, store.ErrUnknownTenant
	}
	snap, ok := tn.snapshots[[3]string{name, key, fingerprint}]
	return snap.Clone(), ok, nil
}
