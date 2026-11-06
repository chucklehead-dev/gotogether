package sim_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/edgepublish/publisher"
	"github.com/chucklehead-dev/gotogether/examples/edgepublish/sim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qtest"
	"hegel.dev/go/hegel"
)

const binding = "../binding.yaml"

// woven skips unless the build wove the publisher (orchestrion go test):
// without it nothing records, and its real locks would block the
// simulation's scheduler.
func woven(t *testing.T) {
	t.Helper()
	qtest.RequireQuint(t)
	if !sim.Woven() {
		t.Skip("needs the build woven: go run github.com/DataDog/orchestrion go test ./sim/")
	}
}

func explore(t *testing.T, cfg sim.Config, seeds int, opts ...qtest.Option) *qtest.Exploration {
	sys := qtest.System{Binding: binding, Opts: opts, Run: func(c qenv.Chooser) error { return sim.Run(c, cfg) }}
	return sys.Explore(t, qtest.ExploreConfig{Seeds: seeds})
}

// Every interleaving of pushes, rotations and background seals, with S3
// writes that fail or fail ambiguously and crashes, is a behaviour of the
// model: the publisher's protocol conforms (checked without invariants).
func TestExploredRunsConform(t *testing.T) {
	woven(t)
	explore(t, sim.Config{}, 40, qtest.Invariants()).Expect(t, qtest.Conforms())
}

// With plain failures only, the design's invariants hold too.
func TestInvariantsHoldWithoutAmbiguousWrites(t *testing.T) {
	woven(t)
	explore(t, sim.Config{NoAmbiguous: true}, 40).Expect(t, qtest.Conforms())
}

// With ambiguous writes, exploration finds the model's known finding on
// its own: a manifest PUT that lands but reports failure is retried as a
// new batch, so the payload is committed twice (F3), and the seal, counted
// in memory, misses the batch it never knew of (F1). The hand-written
// ambiguous-seal scenario reproduced this; here random runs find it.
func TestExplorationFindsAmbiguousFindings(t *testing.T) {
	woven(t)
	explore(t, sim.Config{}, 60).Expect(t, qtest.Some(qtest.AnyOf(qtest.Violates("committedNoDuplicatePayload"), qtest.Violates("sealMatchesManifests"))))
}

// Exploration finds the first two mutants: a manifest written before its
// data, and a rotation that does not wait for in-flight pushes. A directed
// clock-boundary schedule checks the narrower backward-rotation race.
func TestMutants(t *testing.T) {
	woven(t)
	t.Run("ManifestBeforeTable", func(t *testing.T) {
		explore(t, sim.Config{Mutation: publisher.ManifestBeforeTable, NoCrashes: true}, 20, qtest.Invariants()).
			Expect(t, qtest.Some(qtest.Rejects("writeManifest")))
	})
	t.Run("NoRotationLock", func(t *testing.T) {
		explore(t, sim.Config{Mutation: publisher.NoRotationLock, NoCrashes: true}, 40, qtest.Invariants()).
			Expect(t, qtest.Some(qtest.AnyOf(qtest.Rejects("rotateGen"), qtest.Rejects("writeTable"), qtest.Rejects("writeParquet"), qtest.Rejects("writeManifest"))))
	})
	// The old acquire samples the clock before taking the generation lock.
	// Force that sample to return after another push crosses a boundary,
	// instead of relying on unrelated background bookkeeping to expose it.
	t.Run("BackwardRotation", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			mutation publisher.Mutation
			claim    qtest.Claim
		}{
			{"faithful", publisher.Faithful, qtest.Conforms()},
			{"mutant", publisher.BackwardRotation, qtest.Some(qtest.AnyOf(qtest.Rejects("rotateGen"), qtest.Mismatch("rotateGen")))},
		} {
			t.Run(tc.name, func(t *testing.T) {
				sys := qtest.System{Binding: binding, Opts: []qtest.Option{qtest.Invariants()}, Run: func(c qenv.Chooser) error {
					return delayedClockRun(c, tc.mutation)
				}}
				sys.WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 1}).Expect(t, tc.claim)
			})
		}
	})
}

// delayedClockRun models a task descheduled after reading the clock but
// before using that reading. Both implementations get the same delayed old
// reading and a second push after the boundary. The faithful publisher holds
// its shared lock through that delay, then rechecks time under its exclusive
// lock; the mutant lets the second push rotate before using the stale reading.
func delayedClockRun(c qenv.Chooser, mutation publisher.Mutation) error {
	s := qenv.New(c)
	defer s.Close()
	armed, delayed := false, false
	now := func() time.Time {
		at := s.Now()
		if armed && !delayed {
			delayed = true
			qenv.Sleep(s.Context(), 2*sim.Generation)
		}
		return at
	}
	p := publisher.New(publisher.Config{Producer: "edge-1", Region: "eu", Signal: "traces",
		Epoch: "epoch-1", Generation: sim.Generation, Now: now, Mutation: mutation}, publisher.NewMemStore())
	var failures []error
	completed := 0
	s.Spawn("boundary", func(ctx context.Context) {
		if err := p.Tick(ctx); err != nil {
			failures = append(failures, err)
			return
		}
		armed = true
		qenv.Go(ctx, "stale", func(ctx context.Context) {
			if err := p.Push(publisher.WithPayload(ctx, "stale"), []byte("stale")); err != nil {
				failures = append(failures, err)
			}
			completed++
		})
		qenv.Go(ctx, "fresh", func(ctx context.Context) {
			qenv.Sleep(ctx, sim.Generation)
			if err := p.Push(publisher.WithPayload(ctx, "fresh"), []byte("fresh")); err != nil {
				failures = append(failures, err)
			}
			completed++
		})
	})
	if err := s.Run(3 * sim.Generation); err != nil {
		return err
	}
	if !delayed || completed != 2 {
		return fmt.Errorf("clock-boundary prerequisites not completed: delayed=%v, pushes=%d", delayed, completed)
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	s.Spawn("close", func(ctx context.Context) {
		if err := p.Close(ctx); err != nil {
			failures = append(failures, err)
		}
	})
	if err := s.Settle(sim.Generation); err != nil {
		return err
	}
	return errors.Join(failures...)
}

// Hegel makes every choice (interleaving, latencies, faults) and shrinks
// NoRotationLock to a rotation while a push is between its writes.
// BackwardRotation uses the directed clock-boundary schedule above.
func TestHegelShrinksNoRotationLock(t *testing.T) {
	woven(t)
	if testing.Short() {
		t.Skip("shrinking against the model")
	}
	v := qhegel.FindViolation(t, qhegel.ViolationConfig{Binding: binding}, func(tc hegel.TestCase, rec *qhegel.Case) {
		c := rec.Env(qhegel.Choices(tc))
		if err := sim.Run(c, sim.Config{Mutation: publisher.NoRotationLock, NoCrashes: true, NoAmbiguous: true, Requests: 4}); err != nil {
			tc.Errorf("%v", err)
		}
	}, hegel.WithTestCases(80))
	v.Expect(t, qtest.Rejects("rotateGen"))
	t.Logf("shrunk to %d steps:\n%s", len(v.Steps), qhegel.Render(v.Steps))
}
