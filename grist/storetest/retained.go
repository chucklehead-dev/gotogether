package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// Imported history keeps exact IDs. Invalid/colliding IDs reject the whole
// batch before its events, marker, checkpoint or step row can commit.
func retainedIdentities(ctx context.Context, s store.Store) error {
	for _, tenant := range []string{"t1", "t2"} {
		if err := s.CreateTenant(ctx, tenant); err != nil {
			return err
		}
	}
	ev := store.Event{Type: "a-happened", Version: 1, Body: []byte(`{}`)}
	first, second := fmt.Sprintf("%032x", 1), fmt.Sprintf("%032x", 2)
	retained := []string{first, second}
	if _, err := s.Append(ctx, "t1", store.Batch{Events: []store.Event{ev, ev}, RetainedIDs: retained}); err != nil {
		return fmt.Errorf("retained identities: import: %w", err)
	}
	retained[0] = "caller reused the identity slice"
	selector := store.Selector{Types: []string{ev.Type}}
	events, _, err := s.Read(ctx, "t1", selector, 0)
	if err != nil {
		return err
	}
	if len(events) != 2 || events[0].ID != first || events[1].ID != second {
		return fmt.Errorf("retained identities: import did not preserve original identities")
	}
	if _, err := s.Append(ctx, "t1", store.Batch{Events: []store.Event{ev}}); err != nil {
		return err
	}
	before, _, err := s.Read(ctx, "t1", selector, 0)
	if err != nil {
		return err
	}
	if len(before) != 3 || !same(before[:2], events) {
		return fmt.Errorf("retained identities: normal append changed imported events")
	}
	if err := checkIDs(before); err != nil {
		return err
	}
	log, hasSteps := s.(store.StepLog)
	var stepCount int64
	if hasSteps {
		if stepCount, err = log.StepCount(ctx, "t1"); err != nil {
			return err
		}
	}
	for i, tc := range []struct {
		tenant string
		events []store.Event
		ids    []string
	}{
		{"t1", []store.Event{ev}, []string{}},
		{"t1", []store.Event{ev}, []string{""}},
		{"t1", []store.Event{ev}, []string{strings.Repeat("A", 32)}},
		{"t1", []store.Event{ev, ev}, []string{fmt.Sprintf("%032x", 4), fmt.Sprintf("%032x", 4)}},
		{"t1", []store.Event{ev}, []string{first}},
		// The first identity is fresh, the second collides; no prefix commits.
		{"t1", []store.Event{ev, ev}, []string{fmt.Sprintf("%032x", 100), first}},
		{"t2", []store.Event{ev}, []string{first}},
	} {
		marker := fmt.Sprintf("invalid-retained-%d", i)
		to := store.Position(1)
		if tc.tenant == "t2" {
			to = 0
		}
		_, err := s.Append(ctx, tc.tenant, store.Batch{Events: tc.events, RetainedIDs: tc.ids,
			Marker: &store.Marker{ID: marker}, Step: []byte(`{"invalid":true}`),
			Checkpoint: &store.CheckpointMove{Processor: "retained", From: 0, To: to}})
		var permanent *store.PermanentError
		if !errors.As(err, &permanent) || store.MayHaveCommitted(err) {
			return fmt.Errorf("retained identities: invalid batch %d returned %v, want permanent non-commit", i, err)
		}
		if i >= 4 {
			var collision *store.IdentityExists
			if !errors.As(err, &collision) || collision.ID != first {
				return fmt.Errorf("retained identities: collision batch %d returned %v, want typed identity %s", i, err, first)
			}
		}
		if _, found, err := s.Marker(ctx, tc.tenant, marker); err != nil || found {
			return fmt.Errorf("retained identities: invalid batch %d committed its marker: %v", i, err)
		}
	}
	after, _, err := s.Read(ctx, "t1", selector, 0)
	if err != nil {
		return err
	}
	if !same(after, before) {
		return fmt.Errorf("retained identities: invalid batch changed existing history")
	}
	heads, err := s.Heads(ctx)
	if err != nil || heads["t1"] != 3 || heads["t2"] != 0 {
		return fmt.Errorf("retained identities: invalid batch moved a tenant head: %v %v", heads, err)
	}
	if hasSteps {
		if n, err := log.StepCount(ctx, "t1"); err != nil || n != stepCount {
			return fmt.Errorf("retained identities: invalid batch changed its step log: %d %v", n, err)
		}
	}
	for _, tenant := range []string{"t1", "t2"} {
		if cp, err := s.Checkpoint(ctx, tenant, "retained"); err != nil || cp != 0 {
			return fmt.Errorf("retained identities: invalid batch moved %s's checkpoint: %d %v", tenant, cp, err)
		}
	}
	return nil
}

// Distinct tenants serialize an imported identity globally, even though they
// lock different heads. The identity lookup cannot be an unlocked preflight.
func concurrentRetainedIdentities(ctx context.Context, s store.Store) error {
	tenants := []string{"t1", "t2"}
	for _, tenant := range tenants {
		if err := s.CreateTenant(ctx, tenant); err != nil {
			return err
		}
	}
	id := strings.Repeat("b", 32)
	var done atomic.Int32
	errorsByTenant := make([]error, len(tenants))
	for i, tenant := range tenants {
		qenv.Go(ctx, "storetest.retained-"+tenant, func(ctx context.Context) {
			defer done.Add(1)
			batch := store.Batch{Events: []store.Event{{Type: "a-happened", Version: 1, Body: []byte(`{}`)}},
				RetainedIDs: []string{id}, Marker: &store.Marker{ID: "retained-claim"}}
			for try := 0; ; try++ {
				_, err := s.Append(ctx, tenant, batch)
				var exists *store.IdentityExists
				if err == nil || errors.As(err, &exists) {
					errorsByTenant[i] = err
					return
				}
				// A commit answer can be lost. Its marker proves which
				// tenant won without trying to append the event again.
				if _, committed, markerErr := s.Marker(ctx, tenant, "retained-claim"); markerErr == nil && committed {
					return
				}
				if !store.Transient(err) || try >= 20 {
					errorsByTenant[i] = err
					return
				}
				qenv.Sleep(ctx, time.Millisecond)
			}
		})
	}
	if !qenv.WaitFor(ctx, func() bool { return done.Load() == 2 }, time.Minute) {
		return fmt.Errorf("retained identities: concurrent claimants did not finish")
	}
	winners := 0
	for i, err := range errorsByTenant {
		if err == nil {
			winners++
			continue
		}
		var exists *store.IdentityExists
		if !errors.As(err, &exists) || exists.ID != id || store.MayHaveCommitted(err) {
			return fmt.Errorf("retained identities: tenant %s's competing claim: %v, want a known non-commit identity collision", tenants[i], err)
		}
	}
	heads, err := s.Heads(ctx)
	if err != nil || winners != 1 || heads["t1"]+heads["t2"] != 1 {
		return fmt.Errorf("retained identities: %d concurrent claims won, heads %v (%v), want exactly one committed event", winners, heads, err)
	}
	return nil
}
