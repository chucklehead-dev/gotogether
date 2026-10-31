package grist

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Reused log positions identify new triggers after restore. Every node must
// derive the same new effect key, while events in the retained prefix keep
// theirs. The fixture uses actual backup, rewind detection, resume and replay;
// calling the poller's key path directly removes scheduling from this contract.
func TestRewindEffectIdentitySurvivesNodeRestart(t *testing.T) {
	t.Parallel()
	sink := &qobs.MemorySink{}
	recorder := qobs.NewRecorder(sink, qobs.WithID("rewind-identity"))
	t.Cleanup(func() {
		if err := recorder.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := qobs.WithRecorder(context.Background(), recorder)
	s := memstore.New("rewind-identity")
	const tenant = "tenant"
	if err := s.CreateTenant(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	appendTrigger := func(id string) Position {
		t.Helper()
		head, err := s.Append(ctx, tenant, store.Batch{
			Events: []store.Event{{Type: "trigger", Version: 1, Body: []byte(`{"value":"` + id + `"}`)}},
			Cause:  id,
			Time:   1700000000000,
			Step: encodeRow([]rowStep{newRowStep("appendTrigger", 1,
				"id", id, "obs.causes", []any{[]any{"command", id}})}),
		})
		if err != nil {
			t.Fatal(err)
		}
		return head
	}
	triggerAt := func(p Position) Stored {
		t.Helper()
		events, _, err := s.Read(ctx, tenant, store.Selector{Types: []string{"trigger"}, After: p - 1, Limit: 1}, p)
		if err != nil || len(events) != 1 || events[0].Pos != p {
			t.Fatalf("read trigger %d: %+v (%v)", p, events, err)
		}
		return events[0]
	}
	newRelay := func() *Relay {
		t.Helper()
		r, err := NewRelay(s)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	flush := func(r *Relay) {
		t.Helper()
		if err := r.Flush(ctx); err != nil {
			t.Fatal(err)
		}
	}
	key := func(r *Relay, p Position) string {
		pl := &poller{app: New("rewind-app"), p: &processorInfo{name: "deliver"}, tenant: tenant, clocks: &r.clocks}
		return pl.key(triggerAt(p))
	}
	retained := appendTrigger("retained")
	survivor := newRelay()
	flush(survivor)
	backup := s.Backup()
	retainedKey := key(survivor, retained)
	lost := appendTrigger("same-payload-and-cause")
	lostTrigger := triggerAt(lost)
	flush(survivor)
	previousKey := key(survivor, lost)

	for generation := 1; generation <= 2; generation++ {
		s.Restore(backup)
		flush(survivor)
		if stops := survivor.Rewound(); len(stops) != 1 {
			t.Fatalf("restore %d did not stop surviving node: %+v", generation, stops)
		}
		if err := Resume(ctx, s, tenant); err != nil {
			t.Fatal(err)
		}
		flush(survivor)
		if stops := survivor.Rewound(); len(stops) != 0 {
			t.Fatalf("resume %d left surviving node stopped: %+v", generation, stops)
		}
		replacement := appendTrigger("same-payload-and-cause")
		if replacement != lost {
			t.Fatalf("restore did not reuse trigger position: got %d, want %d", replacement, lost)
		}
		replacementTrigger := triggerAt(replacement)
		if !bytes.Equal(replacementTrigger.Body, lostTrigger.Body) || replacementTrigger.Cause != lostTrigger.Cause || replacementTrigger.Time != lostTrigger.Time {
			t.Fatal("control must replace the event with identical body, cause and time")
		}
		if replacementTrigger.ID == "" || replacementTrigger.ID == lostTrigger.ID {
			t.Fatalf("replacement identity %q reused lost identity %q", replacementTrigger.ID, lostTrigger.ID)
		}
		flush(survivor)
		restarted := newRelay()
		flush(restarted)
		if stops := restarted.Rewound(); len(stops) != 0 {
			t.Fatalf("fresh node claimed an unobserved rewind: %+v", stops)
		}
		for name, relay := range map[string]*Relay{"surviving": survivor, "restarted": restarted} {
			if got := key(relay, retained); got != retainedKey {
				t.Errorf("generation %d %s node changed retained trigger key: got %q, want %q", generation, name, got, retainedKey)
			}
		}
		survivingKey, restartedKey := key(survivor, replacement), key(restarted, replacement)
		if survivingKey == previousKey {
			t.Errorf("generation %d reused lost trigger key %q", generation, previousKey)
		}
		if survivingKey != restartedKey {
			t.Errorf("generation %d replacement trigger keys disagree: surviving %q, restarted %q", generation, survivingKey, restartedKey)
		}
		previousKey = survivingKey
	}
}

// A real processor's public Meta and effect context agree on trigger identity.
// The effect succeeds before a scripted crash loses its checkpoint, fails once
// after restart, then succeeds: every retry belongs to the same stored event.
func TestEffectContextIdentitySurvivesCrashAndRetry(t *testing.T) {
	t.Parallel()
	type trigger struct {
		Value int `json:"value"`
	}
	type delivered struct {
		Value int `json:"value"`
	}
	a := New("identity-effects")
	triggers := NewEvent[trigger](a, "trigger")
	deliveries := NewEvent[delivered](a, "delivered")
	var metadataIDs, effectKeys []string
	NewProcessor(a, "deliver", triggers, func(ctx context.Context, reads *Reads, event trigger, meta Meta) Result {
		metadataIDs = append(metadataIDs, meta.ID)
		return Effect(func(ctx context.Context) error {
			effectKeys = append(effectKeys, EffectKey(ctx))
			if len(effectKeys) == 2 {
				return errors.New("transient dependency refusal")
			}
			return nil
		}, AtLeastOnce, OnSuccess(deliveries.Of(delivered{Value: event.Value})))
	})
	chooser := qenv.NewSeeded(17)
	chooser.Rates = qenv.NoFaults
	script := &qenv.Script{Base: chooser, Faults: []qenv.Scripted{{Kind: "grist.deliver.effect", Name: "crashAfter"}}}
	sink := &qobs.MemorySink{}
	recorder := qobs.NewRecorder(sink, qobs.WithID("effect-context-identity"))
	ctx := qobs.WithRecorder(context.Background(), recorder)
	sim := qenv.New(qenv.WithContext(script, ctx))
	sim.Check = true
	defer sim.Close()
	t.Cleanup(func() {
		if err := recorder.Close(); err != nil {
			t.Error(err)
		}
	})
	st := simstore.New(qenv.NewStore(sim, "identity-effects", qenv.Serializable))
	ctx = sim.Context()
	if err := st.CreateTenant(ctx, "tenant"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, "tenant", store.Batch{Events: []Event{triggers.Of(trigger{Value: 7})}, Time: 1700000000000, Cause: "command"}); err != nil {
		t.Fatal(err)
	}
	before, _, err := st.Read(ctx, "tenant", store.Selector{Types: []string{"trigger"}}, 0)
	if err != nil || len(before) != 1 || before[0].ID == "" {
		t.Fatalf("stored trigger identity missing: %+v (%v)", before, err)
	}
	node := sim.Process("node", true, func(ctx context.Context) {
		if err := a.Start(ctx, st); err != nil {
			t.Error(err)
		}
	})
	crashes := 0
	sim.OnCrash(func(process *qenv.Process) {
		if process == node {
			crashes++
			process.Restart()
		}
	})
	if err := sim.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if crashes != 1 || len(script.Remaining()) != 0 {
		t.Fatalf("required crash did not occur once: crashes=%d remaining=%+v", crashes, script.Remaining())
	}
	if len(effectKeys) != 3 || len(metadataIDs) != 2 {
		t.Fatalf("effect/handler attempts=%d/%d, want 3/2 across crash and retry", len(effectKeys), len(metadataIDs))
	}
	for _, id := range metadataIDs {
		if id != before[0].ID {
			t.Fatalf("handler Meta.ID %q, want persisted %q", id, before[0].ID)
		}
	}
	for _, key := range effectKeys {
		if key != effectKeys[0] || !strings.HasSuffix(key, "@"+before[0].ID) {
			t.Fatalf("effect identity changed or omitted persisted ID: %+v", effectKeys)
		}
	}
	log, _, err := st.Read(ctx, "tenant", store.Selector{Types: []string{"trigger", "delivered", "grist.failed"}}, 0)
	if err != nil || len(log) != 2 || log[1].Type != "delivered" {
		t.Fatalf("retry did not commit one successful outcome: %+v (%v)", log, err)
	}
	if cp, err := st.Checkpoint(ctx, "tenant", "deliver"); err != nil || cp != before[0].Pos {
		t.Fatalf("checkpoint=%d (%v), want trigger %d", cp, err, before[0].Pos)
	}
}

// Store corruption is not a domain refusal. A malformed identity anywhere in
// a fetched batch must stop processing before even an earlier valid trigger's
// callback runs, and must not checkpoint or append grist.failed evidence.
func TestInvalidTriggerIdentityStopsBeforeCallbacks(t *testing.T) {
	t.Parallel()
	for _, invalid := range []string{"", "legacy-position-key", strings.Repeat("A", 32), strings.Repeat("0", 31)} {
		t.Run(invalid, func(t *testing.T) {
			type trigger struct {
				Value int `json:"value"`
			}
			a := New("invalid-identity")
			on := NewEvent[trigger](a, "trigger")
			handlers, effects := 0, 0
			processor := NewProcessor(a, "deliver", on, func(ctx context.Context, reads *Reads, event trigger, meta Meta) Result {
				handlers++
				return Effect(func(context.Context) error { effects++; return nil }, AtLeastOnce)
			})
			sink := &qobs.MemorySink{}
			recorder := qobs.NewRecorder(sink, qobs.WithID("invalid-trigger-identity"))
			t.Cleanup(func() {
				if err := recorder.Close(); err != nil {
					t.Error(err)
				}
			})
			ctx := qobs.WithRecorder(context.Background(), recorder)
			st := memstore.New("invalid-identity")
			if err := st.CreateTenant(ctx, "tenant"); err != nil {
				t.Fatal(err)
			}
			if _, err := st.Append(ctx, "tenant", store.Batch{Events: []Event{on.Of(trigger{Value: 1}), on.Of(trigger{Value: 2})}, Time: 1700000000000, Cause: "command"}); err != nil {
				t.Fatal(err)
			}
			fetched, head, err := st.Read(ctx, "tenant", store.Selector{Types: []string{"trigger"}}, 0)
			if err != nil || len(fetched) != 2 {
				t.Fatalf("read triggers: %+v (%v)", fetched, err)
			}
			fetched[1].ID = invalid // Simulate a corrupt store's fetched evidence.
			relay, err := NewRelay(st)
			if err != nil {
				t.Fatal(err)
			}
			pl := &poller{app: a, s: st, p: processor.info, tenant: "tenant", relay: relay, clocks: &relay.clocks, effects: new(atomic.Int64)}
			pl.handle(ctx, 0, head, fetched)
			if handlers != 0 || effects != 0 {
				t.Fatalf("corrupt batch invoked callbacks: handlers=%d effects=%d", handlers, effects)
			}
			if err := relay.Err(); err == nil || !strings.Contains(err.Error(), "invalid immutable ID") || !relay.isStopped("tenant") {
				t.Fatalf("corruption did not retain a stopping data diagnostic: %v", err)
			}
			if cp, err := st.Checkpoint(ctx, "tenant", "deliver"); err != nil || cp != 0 {
				t.Fatalf("corruption advanced checkpoint: %d (%v)", cp, err)
			}
			if rows, err := st.StepCount(ctx, "tenant"); err != nil || rows != 0 {
				t.Fatalf("corruption committed judgment evidence: %d rows (%v)", rows, err)
			}
			all, newHead, err := st.Read(ctx, "tenant", store.Selector{Types: []string{"trigger", "grist.failed", "grist.unknown"}}, 0)
			if err != nil || newHead != head || len(all) != 2 {
				t.Fatalf("corruption appended a domain outcome: %+v head=%d (%v)", all, newHead, err)
			}
		})
	}
}
