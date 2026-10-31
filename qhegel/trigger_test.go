package qhegel

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qload"
	"hegel.dev/go/hegel"
)

func triggerTestOptions() []hegel.Option {
	return []hegel.Option{hegel.WithTestCases(30), hegel.WithSeed(1), hegel.WithVerbosity(hegel.VerbosityQuiet), hegel.WithReproductionBlob(false)}
}

// The final public Hegel.Run replay must propagate our marker; earlier
// candidates or an ordinary nonnil Hegel error must never manufacture Found.
func TestFindTriggerReproducesShrunkFailure(t *testing.T) {
	sys := func(tr qload.Trigger, seed uint64) qload.Recovery {
		return qload.Recovery{Trigger: tr, Recovered: tr.ExtraRate < 1.7}
	}
	s := FindTrigger(t, sys, qload.TriggerSpace{MaxExtraRate: 4, MaxDuration: time.Second, Seeds: 3}, triggerTestOptions()...)
	if s.Err != nil || s.Found == nil || s.Found.Err != nil || s.Found.Recovered {
		t.Fatalf("expected reproduced trigger, got %+v", s)
	}
	if s.Found.Trigger.ExtraRate != 1.7 {
		t.Fatalf("trigger was not shrunk to threshold: %v", s.Found.Trigger)
	}
	if r := sys(s.Found.Trigger, 1); r.Recovered || r.Err != nil {
		t.Fatalf("reported trigger does not reproduce: %v", r)
	}
	s.Expect(t, qload.SomeTrigger())
}

func TestFindTriggerNoFailure(t *testing.T) {
	s := FindTrigger(t, func(tr qload.Trigger, seed uint64) qload.Recovery {
		return qload.Recovery{Trigger: tr, Recovered: true}
	}, qload.TriggerSpace{MaxDuration: time.Second, Seeds: 1}, triggerTestOptions()...)
	if s.Err != nil || s.Found != nil || s.Runs == 0 {
		t.Fatalf("expected completed no-trigger search, got %+v", s)
	}
	s.Expect(t, qload.NoTrigger())
}

func TestFindTriggerUnjudgedBatchHasNoClaim(t *testing.T) {
	unjudged := errors.New("experiment baseline unavailable")
	for _, majorityRecovers := range []bool{false, true} {
		t.Run(fmt.Sprint(majorityRecovers), func(t *testing.T) {
			s := FindTrigger(t, func(tr qload.Trigger, seed uint64) qload.Recovery {
				if seed == 2 {
					return qload.Recovery{Trigger: tr, Err: unjudged}
				}
				return qload.Recovery{Trigger: tr, Recovered: majorityRecovers}
			}, qload.TriggerSpace{MaxDuration: time.Second, Seeds: 3}, triggerTestOptions()...)
			if !errors.Is(s.Err, unjudged) || s.Found != nil || s.Runs == 0 {
				t.Fatalf("unjudged batch established trigger evidence: %+v", s)
			}
			for _, claim := range []qload.TriggerClaim{qload.SomeTrigger(), qload.NoTrigger()} {
				assertTriggerClaimFails(t, s, claim)
			}
		})
	}
}

// A candidate that fails only once cannot be a completed trigger search. Hegel
// reports the unstable property as a run failure, even though an earlier body
// invocation observed non-recovery.
func TestFindTriggerHegelFailureAfterCandidate(t *testing.T) {
	var calls atomic.Int64
	s := FindTrigger(t, func(tr qload.Trigger, seed uint64) qload.Recovery {
		return qload.Recovery{Trigger: tr, Recovered: calls.Add(1) > 1}
	}, qload.TriggerSpace{MaxDuration: time.Second, Seeds: 1}, triggerTestOptions()...)
	if s.Err == nil || s.Found != nil || calls.Load() < 2 {
		t.Fatalf("unstable candidate became trigger evidence: %+v (%d calls)", s, calls.Load())
	}
}

func TestFindTriggerMissingHealthyBaseline(t *testing.T) {
	s := FindTrigger(t, qload.Of(qload.Experiment{}, "paid", time.Second), qload.TriggerSpace{MaxDuration: time.Second, Seeds: 1}, triggerTestOptions()...)
	if s.Err == nil || !strings.Contains(s.Err.Error(), "Experiment.Healthy") || s.Found != nil {
		t.Fatalf("missing healthy baseline became trigger evidence: %+v", s)
	}
	for _, claim := range []qload.TriggerClaim{qload.SomeTrigger(), qload.NoTrigger()} {
		assertTriggerClaimFails(t, s, claim)
	}
}

func TestFindTriggerUnjudgedReplayOverridesEarlierCandidate(t *testing.T) {
	unjudged := errors.New("experiment failed after the first candidate")
	var calls atomic.Int64
	s := FindTrigger(t, func(tr qload.Trigger, seed uint64) qload.Recovery {
		if calls.Add(1) == 1 {
			return qload.Recovery{Trigger: tr}
		}
		return qload.Recovery{Trigger: tr, Err: unjudged}
	}, qload.TriggerSpace{MaxDuration: time.Second, Seeds: 1}, triggerTestOptions()...)
	if !errors.Is(s.Err, unjudged) || s.Found != nil || calls.Load() < 2 {
		t.Fatalf("earlier candidate survived an unjudged replay: %+v", s)
	}
}

func TestFindTriggerInvalidSpaceDoesNotRunTarget(t *testing.T) {
	spaces := []qload.TriggerSpace{
		{Seeds: -1}, {Start: -1}, {MaxDuration: -1}, {MaxExtraRate: -1},
		{Start: 1, MaxDuration: time.Duration(math.MaxInt64)},
		{MaxExtraRate: math.NaN()}, {MaxExtraRate: math.Inf(1)}, {MaxExtraRate: math.MaxFloat64},
		{MaxSlowdown: .5}, {MaxSlowdown: -1}, {MaxSlowdown: math.NaN()}, {MaxSlowdown: math.Inf(1)}, {MaxSlowdown: math.MaxFloat64},
	}
	for _, space := range spaces {
		s := FindTrigger(t, func(qload.Trigger, uint64) qload.Recovery {
			t.Fatal("invalid space ran target")
			return qload.Recovery{}
		}, space)
		if s.Err == nil || s.Found != nil || s.Runs != 0 {
			t.Fatalf("invalid space %+v: %+v", space, s)
		}
	}
	if s := FindTrigger(t, nil, qload.TriggerSpace{}); s.Err == nil || s.Found != nil {
		t.Fatalf("nil target: %+v", s)
	}
}

func TestFindTriggerHegelConfigurationErrorHasNoClaim(t *testing.T) {
	s := FindTrigger(t, func(qload.Trigger, uint64) qload.Recovery {
		t.Fatal("invalid Hegel configuration ran target")
		return qload.Recovery{}
	}, qload.TriggerSpace{}, hegel.WithProfile("gotogether-nonexistent-trigger-profile"))
	if s.Err == nil || s.Found != nil || s.Runs != 0 {
		t.Fatalf("Hegel configuration error became trigger evidence: %+v", s)
	}
}

type triggerFatal struct{}
type triggerClaimRecorder struct {
	testing.TB
	message string
}

func (*triggerClaimRecorder) Helper() {}
func (r *triggerClaimRecorder) Fatalf(format string, args ...any) {
	r.message = fmt.Sprintf(format, args...)
	panic(triggerFatal{})
}

func assertTriggerClaimFails(t *testing.T, search *TriggerSearch, claim qload.TriggerClaim) {
	t.Helper()
	r := &triggerClaimRecorder{}
	defer func() {
		if value := recover(); value != (triggerFatal{}) {
			t.Fatalf("error claim did not fail: %v", value)
		}
		if !strings.Contains(r.message, "could not be judged") {
			t.Fatalf("claim failure hid unjudged search: %s", r.message)
		}
	}()
	search.Expect(r, claim)
}
