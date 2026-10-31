package grist

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Channels establish each boundary: the dependency starts, the real store is
// restored, the relay observes the rewind (and optionally resume), then the
// callback returns. No sleep or probabilistic fault chooses the history.
func TestObservedRewindRetiresInflightEffect(t *testing.T) {
	for _, guarantee := range []Guarantee{AtLeastOnce, AtMostOnce} {
		for _, history := range []string{"unchanged", "trigger-retained", "checkpoint-retained", "trigger-replaced"} {
			for _, resumed := range []bool{false, true} {
				if history == "unchanged" && resumed {
					continue
				}
				for _, result := range []string{"success", "unknown", "not-applied"} {
					t.Run(fmt.Sprintf("%v/%s/resumed=%t/result=%s", guarantee, history, resumed, result), func(t *testing.T) {
						type payload struct {
							Value int `json:"value"`
						}
						a := New("inflight")
						trigger := NewEvent[payload](a, "trigger")
						delivered := NewEvent[payload](a, "delivered")
						entered, release := make(chan string, 1), make(chan struct{})
						var attempts atomic.Int64
						p := NewProcessor(a, "deliver", trigger, func(ctx context.Context, r *Reads, e payload, m Meta) Result {
							return Effect(func(ctx context.Context) error {
								if attempts.Add(1) == 1 {
									entered <- EffectKey(ctx)
									<-release
								}
								if result != "success" {
									if result == "not-applied" {
										return NotApplied(errors.New("dependency refused"))
									}
									return errors.New("dependency outcome unknown")
								}
								return nil
							}, guarantee, OnSuccess(delivered.Of(e)))
						}, EffectTries(2))
						sink := &qobs.MemorySink{}
						rec := qobs.NewRecorder(sink)
						defer rec.Close()
						waitCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
						defer cancel()
						ctx := qobs.WithRecorder(waitCtx, rec)
						s := memstore.New("inflight")
						if err := s.CreateTenant(ctx, "tenant"); err != nil {
							t.Fatal(err)
						}
						backup := s.Backup()
						appendEvent := func(e Event) {
							t.Helper()
							obs, err := a.batchObs([]Event{e}, "command", "fixture")
							if err != nil {
								t.Fatal(err)
							}
							if _, err := s.Append(ctx, "tenant", store.Batch{Events: []Event{e}, Cause: "fixture", Step: encodeRow([]rowStep{newRowStep("fixture", 1, obs...)})}); err != nil {
								t.Fatal(err)
							}
						}
						appendEvent(trigger.Of(payload{Value: 1}))
						events, head, err := s.Read(ctx, "tenant", store.Selector{Types: []string{"trigger"}}, 0)
						if err != nil || len(events) != 1 {
							t.Fatalf("trigger: %+v %v", events, err)
						}
						original := events[0]
						if history != "trigger-replaced" {
							backup = s.Backup()
						}
						relay, err := NewRelay(s)
						if err != nil {
							t.Fatal(err)
						}
						relay.RenderAs(a)
						flush := func() {
							t.Helper()
							if err := relay.Flush(ctx); err != nil {
								t.Fatal(err)
							}
						}
						flush()
						pl := &poller{app: a, s: s, p: p.info, tenant: "tenant", relay: relay, clocks: &relay.clocks, effects: new(atomic.Int64)}
						pinned := withBase(ctx, "tenant", relay.clocks.base("tenant"))
						done := make(chan struct{})
						var releaseOnce sync.Once
						releaseCallback := func() { releaseOnce.Do(func() { close(release) }) }
						go func() { defer close(done); pl.handle(pinned, 0, head, events) }()
						defer func() {
							releaseCallback()
							select {
							case <-done:
							case <-waitCtx.Done():
								t.Error("callback did not retire before deadline")
							}
						}()
						var key string
						select {
						case key = <-entered:
						case <-waitCtx.Done():
							t.Fatal("callback did not enter before deadline")
						}
						flush()
						if history == "checkpoint-retained" {
							backup = s.Backup()
						}
						appendEvent(trigger.Of(payload{Value: 2}))
						flush()
						if history != "unchanged" {
							s.Restore(backup)
							flush()
							if len(relay.Rewound()) != 1 {
								t.Fatal("restore was not observed before callback returned")
							}
							if history == "trigger-replaced" {
								appendEvent(trigger.Of(payload{Value: 1}))
								replacement, _, err := s.Read(ctx, "tenant", store.Selector{Types: []string{"trigger"}}, 0)
								if err != nil || len(replacement) != 1 || replacement[0].Pos != original.Pos || replacement[0].ID == original.ID {
									t.Fatalf("replacement: %+v %v", replacement, err)
								}
								flush()
							}
							if resumed {
								if err := Resume(ctx, s, "tenant"); err != nil {
									t.Fatal(err)
								}
								flush()
								if len(relay.Rewound()) != 0 {
									t.Fatal("resume was not observed before callback returned")
								}
							}
						}
						before, _, err := s.Read(ctx, "tenant", store.Selector{Types: []string{"trigger", "delivered", failedType, unknownType}}, 0)
						if err != nil {
							t.Fatal(err)
						}
						cpBefore, err := s.Checkpoint(ctx, "tenant", "deliver")
						if err != nil {
							t.Fatal(err)
						}
						releaseCallback()
						select {
						case <-done:
						case <-waitCtx.Done():
							t.Fatal("callback did not retire before deadline")
						}
						after, _, err := s.Read(ctx, "tenant", store.Selector{Types: []string{"trigger", "delivered", failedType, unknownType}}, 0)
						if err != nil {
							t.Fatal(err)
						}
						cpAfter, err := s.Checkpoint(ctx, "tenant", "deliver")
						if err != nil {
							t.Fatal(err)
						}
						if history == "unchanged" {
							wantAttempts := int64(1)
							if guarantee == AtLeastOnce && result != "success" {
								wantAttempts = 2
							}
							if attempts.Load() != wantAttempts || len(after) != len(before)+1 || cpAfter != original.Pos {
								t.Fatalf("normal processing changed: attempts=%d want=%d events=%d -> %d checkpoint=%d", attempts.Load(), wantAttempts, len(before), len(after), cpAfter)
							}
							return
						}
						if attempts.Load() != 1 {
							t.Fatalf("old invocation retried: %d attempts", attempts.Load())
						}
						if len(after) != len(before) || cpAfter != cpBefore {
							t.Fatalf("old invocation committed: events %d -> %d, checkpoint %d -> %d", len(before), len(after), cpBefore, cpAfter)
						}
						var completion, rewindOrder int64
						completions := 0
						for _, step := range sink.Steps() {
							if step.Action == "gristRewound" {
								rewindOrder = step.Order["pos"]
							}
							if step.Action == "deliverEffect" {
								completions++
								completion = step.Order["pos"]
								if step.Args["triggerId"] != original.ID || step.Args["key"] != key || step.Args["ok"] != (result == "success") {
									t.Fatalf("completion changed original evidence: %+v", step)
								}
							}
						}
						if completions != 1 {
							t.Fatalf("completion evidence count=%d", completions)
						}
						// A completed invocation remains on its pinned old logical clock.
						// It does not need a generalized model exception while rewound.
						if completion >= rewindOrder {
							t.Fatalf("completion order %d is not before rewind %d", completion, rewindOrder)
						}
					})
				}
			}
		}
	}
}

// Only the standalone AMO outcome append is intercepted; its checkpoint uses
// the real store. The first attempt reports a transient noncommit. A rewind
// observed before the retry retires this work even if resume already followed.
type inflightOutcomeStore struct {
	*memstore.Store
	outcome func(context.Context, string, store.Batch) (Position, error)
}

func (s *inflightOutcomeStore) Append(ctx context.Context, tenant string, b store.Batch) (Position, error) {
	if b.Marker != nil && b.Marker.Hash == "outcome" {
		return s.outcome(ctx, tenant, b)
	}
	return s.Store.Append(ctx, tenant, b)
}

func TestObservedRewindRetiresAtMostOnceOutcomeRetry(t *testing.T) {
	for _, retired := range []bool{false, true} {
		t.Run(fmt.Sprintf("retired=%t", retired), func(t *testing.T) {
			type payload struct {
				Value int `json:"value"`
			}
			a := New("outcome-retry")
			trigger := NewEvent[payload](a, "trigger")
			delivered := NewEvent[payload](a, "delivered")
			effects := 0
			p := NewProcessor(a, "deliver", trigger, func(ctx context.Context, r *Reads, e payload, m Meta) Result {
				return Effect(func(context.Context) error { effects++; return nil }, AtMostOnce, OnSuccess(delivered.Of(e)))
			})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			sink := &qobs.MemorySink{}
			recorder := qobs.NewRecorder(sink)
			defer recorder.Close()
			ctx = qobs.WithRecorder(ctx, recorder)
			s := &inflightOutcomeStore{Store: memstore.New("outcome-retry")}
			if err := s.CreateTenant(ctx, "tenant"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Append(ctx, "tenant", store.Batch{Events: []Event{trigger.Of(payload{Value: 1})}}); err != nil {
				t.Fatal(err)
			}
			events, head, err := s.Read(ctx, "tenant", store.Selector{Types: []string{"trigger"}}, 0)
			if err != nil || len(events) != 1 {
				t.Fatalf("trigger: %+v %v", events, err)
			}
			relay, err := NewRelay(s)
			if err != nil {
				t.Fatal(err)
			}
			pl := &poller{app: a, s: s, p: p.info, tenant: "tenant", relay: relay, clocks: &relay.clocks, effects: new(atomic.Int64)}
			attempts := 0
			s.outcome = func(ctx context.Context, tenant string, b store.Batch) (Position, error) {
				attempts++
				if attempts == 1 {
					if retired {
						relay.clocks.stopFor(tenant, &RewoundError{Tenant: tenant, Seen: head, Head: 0})
						relay.clocks.resume(tenant)
					}
					return 0, errors.New("transient outcome append failure")
				}
				return s.Store.Append(ctx, tenant, b)
			}
			// Direct effect execution isolates outcome retry admission from polling.
			res, ok := pl.decide(ctx, events[0])
			if !ok {
				t.Fatal("decide failed")
			}
			pl.effect(ctx, 0, head, events[0], res)
			completions := 0
			for _, step := range sink.Steps() {
				if step.Action == "deliverEffect" {
					completions++
					if step.Args["triggerId"] != events[0].ID || step.Args["key"] != pl.key(events[0]) || step.Args["ok"] != true {
						t.Fatalf("completion changed original evidence: %+v", step)
					}
				}
			}
			if completions != 1 {
				t.Fatalf("completion evidence count=%d", completions)
			}

			want := 2
			if retired {
				want = 1
			}
			if attempts != want || effects != 1 {
				t.Fatalf("outcome attempts=%d want=%d; effects=%d", attempts, want, effects)
			}
			outcomes, _, err := s.Read(ctx, "tenant", store.Selector{Types: []string{"delivered"}}, 0)
			wantOutcomes := 1
			if retired {
				wantOutcomes = 0
			}
			if err != nil || len(outcomes) != wantOutcomes {
				t.Fatalf("outcomes=%+v want=%d: %v", outcomes, wantOutcomes, err)
			}
		})
	}
}
