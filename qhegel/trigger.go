package qhegel

import (
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qload"
	"hegel.dev/go/hegel"
)

// TriggerSearch records one bounded search. Found is the shrunk trigger's
// run, or nil when none was found. Err means the search could not be judged.
type TriggerSearch struct {
	Found *qload.Recovery
	// Err is an invalid search, unjudged recovery batch, or Hegel failure.
	// An errored search never reports Found, even if an earlier case degraded.
	Err  error
	Runs int // test cases run
}

// Expect states what the search should find: qload.SomeTrigger (one that
// breaks the system) or qload.NoTrigger (none found in the tested cases). It fails t
// with the shrunk trigger's run when one was not expected, and returns
// the run. One claim: the two exclude each other.
func (s *TriggerSearch) Expect(t testing.TB, c qload.TriggerClaim) *qload.Recovery {
	t.Helper()
	if s.Err != nil {
		t.Fatalf("qhegel: trigger search could not be judged: %v", s.Err)
		return nil
	}
	found := c.WantsTrigger()
	switch {
	case found && s.Found == nil:
		t.Fatalf("qhegel: no trigger in %d runs breaks the system; want one (widen the space or raise the test cases)", s.Runs)
	case !found && s.Found != nil:
		t.Fatalf("qhegel: found a trigger the system does not recover from; want none: %v", *s.Found)
	}
	return s.Found
}

// FindTrigger searches, with Hegel, for a trigger after which the system
// does not recover, and shrinks it: the extra load, the slowdown and the
// duration all shrink toward none, so the result is a smallest stressor
// that tips the system into a state it stays in. The system is a qload
// simulation (qload.OfSimulation), an application's experiment (qload.Of)
// or anything else; each trigger runs with space.Seeds seeds, and breaks
// the system when most do not recover (qload.Mostly). Unset bounds
// default: no extra load, no slowdown, 300 s; Hegel's options bound the
// search. Found is populated only by the final reproduced, shrunk trigger;
// an earlier candidate is insufficient. An unjudged seed or a Hegel error
// sets Err and leaves Found nil, establishing neither trigger claim.
// State what the search should find with Expect.
func FindTrigger(t testing.TB, sys qload.Target, space qload.TriggerSpace, opts ...hegel.Option) *TriggerSearch {
	t.Helper()
	if space.MaxSlowdown == 0 {
		space.MaxSlowdown = 1
	}
	if space.MaxDuration == 0 {
		space.MaxDuration = 300 * time.Second
	}
	if space.Seeds == 0 {
		space.Seeds = 3
	}
	if err := validateTriggerSpace(sys, space); err != nil {
		return &TriggerSearch{Err: err}
	}
	var mu sync.Mutex
	var batchErr error
	runs := 0
	found, err := runTriggerSearch(func(tc hegel.TestCase) {
		mu.Lock()
		failed := batchErr != nil
		mu.Unlock()
		if failed {
			return // stop running experiments after an unjudged batch
		}
		tr := qload.Trigger{
			Start:     space.Start,
			ExtraRate: float64(hegel.Draw(tc, hegel.Integers(0, int(space.MaxExtraRate*10)))) / 10,
			Slowdown:  1 + float64(hegel.Draw(tc, hegel.Integers(0, int((space.MaxSlowdown-1)*10))))/10,
			Duration:  time.Duration(hegel.Draw(tc, hegel.Integers(0, int(space.MaxDuration/time.Second)))) * time.Second,
		}
		ok, r, err := qload.Mostly(sys, tr, space.Seeds)
		mu.Lock()
		runs++
		if err != nil && batchErr == nil {
			batchErr = err
		}
		mu.Unlock()
		if err != nil {
			tc.Errorf("recovery could not be judged: %v", err)
			return
		}
		if !ok {
			panic(triggerFound{r})
		}
	}, append([]hegel.Option{hegel.SuppressHealthCheck(hegel.TooSlow), hegel.WithDatabase("")}, opts...)...)
	if batchErr != nil {
		return &TriggerSearch{Err: fmt.Errorf("qhegel: recovery batch: %w", batchErr), Runs: runs}
	}
	if err != nil {
		return &TriggerSearch{Err: fmt.Errorf("qhegel: trigger search: %w", err), Runs: runs}
	}
	if found == nil {
		t.Logf("qhegel: no trigger in %d runs breaks the system", runs)
		return &TriggerSearch{Runs: runs}
	}
	t.Logf("qhegel: %d runs; smallest trigger found: %v", runs, *found)
	return &TriggerSearch{Found: found, Runs: runs}
}

// triggerFound distinguishes a reproduced recovery failure from an engine
// failure. Hegel captures user panics while searching and shrinking, then
// propagates the final deterministic replay's panic. A callback's earlier
// candidate alone is not evidence of a completed, reproducible search.
type triggerFound struct{ recovery qload.Recovery }

func runTriggerSearch(body func(hegel.TestCase), opts ...hegel.Option) (found *qload.Recovery, err error) {
	defer func() {
		if value := recover(); value != nil {
			if failure, ok := value.(triggerFound); ok {
				found = &failure.recovery
				return
			}
			panic(value)
		}
	}()
	err = hegel.Run(body, opts...)
	return
}

func validateTriggerSpace(sys qload.Target, space qload.TriggerSpace) error {
	switch {
	case sys == nil:
		return fmt.Errorf("qhegel: trigger target is nil: supply a recovery experiment")
	case space.Seeds <= 0:
		return fmt.Errorf("qhegel: trigger seeds must be positive: got %d", space.Seeds)
	case space.Start < 0:
		return fmt.Errorf("qhegel: trigger start must not be negative: got %s", space.Start)
	case space.MaxDuration < 0:
		return fmt.Errorf("qhegel: maximum trigger duration must not be negative: got %s", space.MaxDuration)
	case space.MaxDuration > time.Duration(math.MaxInt64)-space.Start:
		return fmt.Errorf("qhegel: trigger start and maximum duration overflow time.Duration: reduce the search bounds")
	}
	// Draw bounds must be finite and fit int before conversion; float64(maxInt)
	// rounds upward on 64-bit machines, so equality is also refused.
	maxInt := float64(int(^uint(0) >> 1))
	if math.IsNaN(space.MaxExtraRate) || math.IsInf(space.MaxExtraRate, 0) || space.MaxExtraRate < 0 || space.MaxExtraRate*10 >= maxInt {
		return fmt.Errorf("qhegel: maximum extra rate must be finite, nonnegative and fit the draw range: got %g", space.MaxExtraRate)
	}
	if math.IsNaN(space.MaxSlowdown) || math.IsInf(space.MaxSlowdown, 0) || space.MaxSlowdown < 1 || (space.MaxSlowdown-1)*10 >= maxInt {
		return fmt.Errorf("qhegel: maximum slowdown must be finite, at least 1 and fit the draw range: got %g", space.MaxSlowdown)
	}
	return nil
}
