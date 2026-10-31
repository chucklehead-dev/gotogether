package storetest

import (
	"context"
	"fmt"

	"github.com/chucklehead-dev/gotogether/grist/store"
)

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func checkIDs(events []store.Stored) error {
	seen := map[string]store.Position{}
	for _, e := range events {
		if !validID(e.ID) {
			return fmt.Errorf("event identities: event at position %d has invalid identity %q", e.Pos, e.ID)
		}
		if p, ok := seen[e.ID]; ok {
			return fmt.Errorf("event identities: events at positions %d and %d share identity %s", p, e.Pos, e.ID)
		}
		seen[e.ID] = e.Pos
		if e.Meta().ID != e.ID || e.Clone().ID != e.ID {
			return fmt.Errorf("event identities: event at position %d lost its identity in metadata or clone", e.Pos)
		}
	}
	return nil
}

// assignedIDs enriches the independent event/envelope reference with the IDs
// observed once after append. Later reads compare these exact IDs too.
func assignedIDs(expected, observed []store.Stored) error {
	if len(expected) != len(observed) {
		return fmt.Errorf("event identities: initial read returned %d events, want %d", len(observed), len(expected))
	}
	if err := checkIDs(observed); err != nil {
		return err
	}
	for i := range expected {
		if expected[i].Pos != observed[i].Pos {
			return fmt.Errorf("event identities: initial read position %d, want %d", observed[i].Pos, expected[i].Pos)
		}
		expected[i].ID = observed[i].ID
	}
	return nil
}

// Identical payloads and envelopes still name distinct committed events. A
// reader must return the stored identities, never mint one on each read.
func eventIdentities(ctx context.Context, s store.Store) error {
	if err := s.CreateTenant(ctx, "t1"); err != nil {
		return err
	}
	ev := store.Event{Type: "a-happened", Version: 1, Body: []byte(`{"same":true}`)}
	selector := store.Selector{Types: []string{ev.Type}}
	var before []store.Stored
	for batch := 0; batch < 2; batch++ {
		if _, err := s.Append(ctx, "t1", store.Batch{Events: []store.Event{ev, ev, ev}, Time: 17, Cause: "same-cause"}); err != nil {
			return err
		}
		events, _, err := s.Read(ctx, "t1", selector, 0)
		if err != nil {
			return err
		}
		if len(events) != 3*(batch+1) {
			return fmt.Errorf("event identities: got %d events after batch %d", len(events), batch)
		}
		if err := checkIDs(events); err != nil {
			return err
		}
		if !same(events[:len(before)], before) {
			return fmt.Errorf("event identities: an append changed retained event identities")
		}
		before = events
	}
	for _, q := range []store.Selector{selector, {Types: selector.Types, Reverse: true}, {Types: selector.Types, After: 1, Limit: 3}} {
		got, _, err := s.Read(ctx, "t1", q, 0)
		if err != nil {
			return err
		}
		if !same(got, store.Select(before, q, 6)) {
			return fmt.Errorf("event identities: repeated or selected read changed stored identities")
		}
	}
	return nil
}
