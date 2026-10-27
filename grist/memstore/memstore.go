// Package memstore is a grist.Store in memory: maps and a mutex, for unit
// tests of a fold or a decision. It is not for simulations (its mutex is
// a real one, which qenv cannot schedule): use grist/simstore there.
package memstore

import (
	"context"
	"sort"
	"sync"

	"github.com/chucklehead-dev/gotogether/grist"
)

// Store is an in-memory grist.Store.
type Store struct {
	name string

	mu      sync.Mutex
	tenants map[string]*tenant
	changed chan struct{} // closed and replaced on every append
}

type tenant struct {
	log         []grist.Stored
	markers     map[string]grist.Marker
	checkpoints map[string]grist.Position
}

// New returns an empty store named name.
func New(name string) *Store {
	return &Store{name: name, tenants: map[string]*tenant{}, changed: make(chan struct{})}
}

// Name implements grist.Store.
func (s *Store) Name() string { return s.name }

// CreateTenant implements grist.Store.
func (s *Store) CreateTenant(_ context.Context, t string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tenants[t] == nil {
		s.tenants[t] = &tenant{markers: map[string]grist.Marker{}, checkpoints: map[string]grist.Position{}}
	}
	return nil
}

// Tenants implements grist.Store.
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

// Read implements grist.Store.
func (s *Store) Read(_ context.Context, t string, q grist.Query, at grist.Position) ([]grist.Stored, grist.Position, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn := s.tenants[t]
	if tn == nil {
		return nil, 0, grist.ErrUnknownTenant
	}
	head := grist.Position(len(tn.log))
	if at == 0 || at > head {
		at = head
	}
	return grist.Select(tn.log, q, at), at, nil
}

// Append implements grist.Store.
func (s *Store) Append(_ context.Context, t string, b grist.Batch) (grist.Position, error) {
	s.mu.Lock()
	tn := s.tenants[t]
	if tn == nil {
		s.mu.Unlock()
		return 0, grist.ErrUnknownTenant
	}
	if b.Marker != nil {
		if _, ok := tn.markers[b.Marker.ID]; ok {
			s.mu.Unlock()
			return 0, &grist.ConditionFailed{Marker: b.Marker.ID}
		}
	}
	if c := b.Checkpoint; c != nil && tn.checkpoints[c.Processor] != c.From {
		s.mu.Unlock()
		return 0, &grist.CheckpointMoved{Processor: c.Processor, Expected: c.From, Found: tn.checkpoints[c.Processor]}
	}
	if b.Condition != nil {
		if err := grist.CheckCondition(tn.log, *b.Condition); err != nil {
			s.mu.Unlock()
			return 0, err
		}
	}
	for _, e := range b.Events {
		e.Tags = append([]grist.TagPair(nil), e.Tags...)
		tn.log = append(tn.log, grist.Stored{Event: e, Tenant: t, Pos: grist.Position(len(tn.log) + 1), Time: b.Time, Cause: b.Cause})
	}
	head := grist.Position(len(tn.log))
	if b.Marker != nil {
		m := *b.Marker
		m.Receipt = grist.NewReceipt(t, head, len(b.Events))
		tn.markers[m.ID] = m
	}
	if c := b.Checkpoint; c != nil {
		tn.checkpoints[c.Processor] = c.To
	}
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
	if b.OnCommit != nil {
		b.OnCommit(head)
	}
	return head, nil
}

// Marker implements grist.Store.
func (s *Store) Marker(_ context.Context, t, id string) (grist.Marker, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn := s.tenants[t]
	if tn == nil {
		return grist.Marker{}, false, grist.ErrUnknownTenant
	}
	m, ok := tn.markers[id]
	return m, ok, nil
}

// Checkpoint implements grist.Store.
func (s *Store) Checkpoint(_ context.Context, t, processor string) (grist.Position, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tn := s.tenants[t]
	if tn == nil {
		return 0, grist.ErrUnknownTenant
	}
	return tn.checkpoints[processor], nil
}

// Heads implements grist.Store.
func (s *Store) Heads(context.Context) (map[string]grist.Position, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]grist.Position{}
	for t, tn := range s.tenants {
		out[t] = grist.Position(len(tn.log))
	}
	return out, nil
}

// Wait implements grist.Store.
func (s *Store) Wait(ctx context.Context, t string, p grist.Position) error {
	for {
		s.mu.Lock()
		tn := s.tenants[t]
		if tn == nil {
			s.mu.Unlock()
			return grist.ErrUnknownTenant
		}
		if grist.Position(len(tn.log)) > p {
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
