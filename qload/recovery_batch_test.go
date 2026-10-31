package qload_test

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qload"
)

// Invalid evidence cannot become either a recovery majority or a degradation
// witness. Exercise an error before and after both kinds of valid majority.
func TestRecoveryBatchesRejectUnjudgedRuns(t *testing.T) {
	failure := errors.New("simulation did not reach its horizon")
	for _, c := range []struct {
		name string
		runs []qload.Recovery
		seed string
	}{
		{"missing_healthy", []qload.Recovery{qload.Assess(qload.Experiment{}, "paid", qload.Trigger{}, time.Second, 1)}, "seed 1"},
		{"recovered_majority_then_error", []qload.Recovery{{Recovered: true}, {Recovered: true}, {Err: failure}}, "seed 3"},
		{"error_then_recovered_majority", []qload.Recovery{{Err: failure}, {Recovered: true}, {Recovered: true}}, "seed 1"},
		{"degraded_majority_then_error", []qload.Recovery{{}, {}, {Err: failure}}, "seed 3"},
		{"error_then_degraded_majority", []qload.Recovery{{Err: failure}, {}, {}}, "seed 1"},
		{"recovered_flag_with_error", []qload.Recovery{{Recovered: true, Err: failure}}, "seed 1"},
		{"lowest_error_seed", []qload.Recovery{{Err: failure}, {Err: errors.New("later failure")}, {}}, "seed 1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var calls atomic.Int64
			target := func(_ qload.Trigger, seed uint64) qload.Recovery {
				calls.Add(1)
				return c.runs[seed-1]
			}
			checkError := func(err error) {
				t.Helper()
				if err == nil || !strings.Contains(err.Error(), c.seed) {
					t.Fatalf("batch error = %v; want lowest unjudged %s", err, c.seed)
				}
				for _, r := range c.runs {
					if r.Err != nil {
						if !errors.Is(err, r.Err) {
							t.Fatalf("error = %v; does not wrap first failed run %v", err, r.Err)
						}
						break
					}
				}
			}
			runs, err := qload.Runs(target, qload.Trigger{}, len(c.runs))
			checkError(err)
			if len(runs) != len(c.runs) {
				t.Fatalf("retained %d runs, want all %d", len(runs), len(c.runs))
			}
			for i, r := range runs {
				if r.Recovered != c.runs[i].Recovered || r.Err != c.runs[i].Err {
					t.Errorf("retained seed %d = %+v; want %+v", i+1, r, c.runs[i])
				}
			}
			n, err := qload.Count(target, qload.Trigger{}, len(c.runs))
			checkError(err)
			if n != 0 {
				t.Errorf("unjudged batch returned recovery count %d", n)
			}
			ok, witness, err := qload.Mostly(target, qload.Trigger{}, len(c.runs))
			checkError(err)
			if ok || witness.Err != nil || witness.Trigger != (qload.Trigger{}) || witness.Recovered {
				t.Errorf("unjudged batch returned verdict/witness: %v %+v", ok, witness)
			}
			if got := calls.Load(); got != int64(3*len(c.runs)) {
				t.Errorf("ran %d seeds; want all %d even when a majority is already known", got, 3*len(c.runs))
			}
		})
	}
}

func TestRecoveryBatchesRequireActualRuns(t *testing.T) {
	var calls atomic.Int64
	target := func(qload.Trigger, uint64) qload.Recovery {
		calls.Add(1)
		return qload.Recovery{Recovered: true}
	}
	for _, c := range []struct {
		name   string
		target qload.Target
		seeds  int
		want   string
	}{{"zero", target, 0, "seeds must be positive"}, {"negative", target, -1, "seeds must be positive"}, {"nil_target", nil, 1, "nil Target"}} {
		t.Run(c.name, func(t *testing.T) {
			check := func(err error) {
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Errorf("error = %v; want %q", err, c.want)
				}
			}
			runs, err := qload.Runs(c.target, qload.Trigger{}, c.seeds)
			check(err)
			if runs != nil {
				t.Errorf("invalid batch retained runs: %+v", runs)
			}
			_, err = qload.Count(c.target, qload.Trigger{}, c.seeds)
			check(err)
			_, _, err = qload.Mostly(c.target, qload.Trigger{}, c.seeds)
			check(err)
		})
	}
	if calls.Load() != 0 {
		t.Fatal("invalid batches started target runs")
	}
}

// Check every clean majority for up to six seeds against its declared bitset,
// including ties, with seed-tagged runs so ordering and the witness matter.
func TestRecoveryMajoritiesAgainstAllSmallBatches(t *testing.T) {
	tr := qload.Trigger{Start: time.Second, Duration: 2 * time.Second, ExtraRate: 3}
	for n := 1; n <= 6; n++ {
		for recovered := 0; recovered < 1<<n; recovered++ {
			count, firstDegraded := 0, 0
			for seed := 1; seed <= n; seed++ {
				if recovered&(1<<(seed-1)) != 0 {
					count++
				} else if firstDegraded == 0 {
					firstDegraded = seed
				}
			}
			target := func(got qload.Trigger, seed uint64) qload.Recovery {
				if got != tr {
					return qload.Recovery{Err: errors.New("target received a different trigger")}
				}
				return qload.Recovery{Trigger: got, Recovered: recovered&(1<<(seed-1)) != 0, Before: float64(seed)}
			}
			runs, err := qload.Runs(target, tr, n)
			if err != nil || len(runs) != n {
				t.Fatalf("n=%d mask=%b: Runs = %v, %v", n, recovered, runs, err)
			}
			for i, r := range runs {
				if r.Before != float64(i+1) {
					t.Fatalf("n=%d mask=%b: run %d belongs to seed %g", n, recovered, i, r.Before)
				}
			}
			got, err := qload.Count(target, tr, n)
			if err != nil || got != count {
				t.Fatalf("n=%d mask=%b: Count = %d, %v; want %d", n, recovered, got, err, count)
			}
			ok, witness, err := qload.Mostly(target, tr, n)
			if err != nil || ok != (2*count >= n) || witness.Before != float64(firstDegraded) {
				t.Fatalf("n=%d mask=%b: Mostly = %v, witness seed %g, %v; want %v, seed %d", n, recovered, ok, witness.Before, err, 2*count >= n, firstDegraded)
			}
		}
	}
}
