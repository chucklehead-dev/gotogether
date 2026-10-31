package storetest

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/chucklehead-dev/gotogether/grist/store"
)

// Exact reads must not depend on the caller knowing an event's type or tags.
func exactEventReads(ctx context.Context, s store.Store) error {
	if _, ok, err := s.ReadEvent(ctx, "missing", 1); ok || !errors.Is(err, store.ErrUnknownTenant) {
		return fmt.Errorf("exact unknown tenant: found=%t error=%v", ok, err)
	}
	for _, tenant := range []string{"exact-a", "exact-b"} {
		if err := s.CreateTenant(ctx, tenant); err != nil {
			return err
		}
	}
	for _, position := range []store.Position{0, -1} {
		if _, ok, err := s.ReadEvent(ctx, "exact-a", position); ok || err == nil || store.Transient(err) {
			return fmt.Errorf("exact invalid position %d: found=%t error=%v; want permanent error", position, ok, err)
		}
	}
	events := []store.Event{
		{Type: "unregistered-first", Version: 3, Body: []byte(`{"value":1}`), Tags: []store.TagPair{{Key: "kind", Value: "one"}}},
		{Type: "unregistered-second", Version: 7, Body: []byte(`{"value":2}`)},
	}
	if _, err := s.Append(ctx, "exact-a", store.Batch{Events: events, Time: 17, Cause: "exact-cause"}); err != nil {
		return err
	}
	if _, err := s.Append(ctx, "exact-b", store.Batch{Events: []store.Event{{Type: "different", Version: 1, Body: []byte(`{}`)}}}); err != nil {
		return err
	}
	selected, _, err := s.Read(ctx, "exact-a", store.Selector{Types: []string{"unregistered-first", "unregistered-second"}}, 0)
	if err != nil || len(selected) != 2 {
		return fmt.Errorf("reference read: %v %v", selected, err)
	}
	for i, want := range selected {
		got, ok, err := s.ReadEvent(ctx, "exact-a", store.Position(i+1))
		if err != nil || !ok || !reflect.DeepEqual(got, want) {
			return fmt.Errorf("exact position %d: got %+v found=%t error=%v; want %+v", i+1, got, ok, err, want)
		}
		got.Body[0] = '!'
		if len(got.Tags) > 0 {
			got.Tags[0].Value = "mutated"
		}
		again, ok, err := s.ReadEvent(ctx, "exact-a", store.Position(i+1))
		if err != nil || !ok || !reflect.DeepEqual(again, want) {
			return fmt.Errorf("exact read shares stored slices: %+v found=%t error=%v", again, ok, err)
		}
	}
	if got, ok, err := s.ReadEvent(ctx, "exact-a", 3); err != nil || ok || !reflect.DeepEqual(got, store.Stored{}) {
		return fmt.Errorf("exact absent position: %+v found=%t error=%v", got, ok, err)
	}
	other, ok, err := s.ReadEvent(ctx, "exact-b", 1)
	if err != nil || !ok || other.Type != "different" || other.ID == selected[0].ID {
		return fmt.Errorf("exact tenant isolation: %+v found=%t error=%v", other, ok, err)
	}
	return nil
}
