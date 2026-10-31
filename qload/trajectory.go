package qload

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/chucklehead-dev/gotogether/qmeta"
)

// SimulateTrajectory averages runs simulations of cfg (with constant load, from
// the state from) into a trajectory for qmeta.Calibrate. runs must be positive;
// times must be nonempty, finite nonnegative whole seconds. Their order and
// duplicates are preserved.
// Time zero is exactly from, before any simulated events. The initial queue must
// fit cfg.Server.QueueCap (zero uses its default of 100), and both counts must be
// nonnegative. Client.Timeout must be positive: the run continues one timeout
// past its final observation so retrospectively counted orbit attempts have
// time to finish. The returned times are an independent copy.
//
// Failed simulations or unavailable requested samples return an error and no
// trajectory: missing evidence is not a zero observation. Horizon conversion is
// checked for overflow; it is not a bound on the simulation's resource use.
func SimulateTrajectory(cfg SimConfig, from qmeta.State, times []float64, runs int) (qmeta.Trajectory, error) {
	if runs <= 0 {
		return qmeta.Trajectory{}, fmt.Errorf("qload: trajectory runs must be positive, got %d: request at least one simulation", runs)
	}
	capacity := cfg.Server.QueueCap
	if capacity == 0 {
		capacity = 100
	}
	if from.U < 0 || from.V < 0 || capacity < 0 || from.U > capacity {
		return qmeta.Trajectory{}, fmt.Errorf("qload: trajectory initial state (%d,%d) must be nonnegative and queue must fit capacity %d: adjust From or Server.QueueCap", from.U, from.V, capacity)
	}
	if len(times) == 0 {
		return qmeta.Trajectory{}, fmt.Errorf("qload: trajectory times are empty: request at least one whole-second observation")
	}
	if cfg.Client.Timeout <= 0 {
		return qmeta.Trajectory{}, fmt.Errorf("qload: trajectory Client.Timeout must be positive, got %v: set a finite attempt timeout so orbit observations can complete", cfg.Client.Timeout)
	}
	if cfg.Client.Timeout > time.Duration(math.MaxInt64)-time.Second {
		return qmeta.Trajectory{}, fmt.Errorf("qload: trajectory timeout plus sampling tail overflows time.Duration: reduce Client.Timeout")
	}
	// An attempt active at the last observation may only be classified as orbit
	// when it times out. One further tick keeps that deadline inside the run.
	tail := cfg.Client.Timeout + time.Second
	// Compare before converting: float64(math.MaxInt64) rounds upward.
	maxSecond := int64(time.Duration(math.MaxInt64)-tail) / int64(time.Second)
	horizon := int64(0)
	for i, t := range times {
		if math.IsNaN(t) || math.IsInf(t, 0) || t < 0 || math.Trunc(t) != t || t > float64(maxSecond) {
			return qmeta.Trajectory{}, fmt.Errorf("qload: trajectory time[%d]=%g must be a finite nonnegative whole second no greater than %d: use the one-second sample grid", i, t, maxSecond)
		}
		horizon = max(horizon, int64(t))
	}
	tr := qmeta.Trajectory{From: from, Times: slices.Clone(times), Mean: make([]qmeta.State2, len(times))}
	cfg.Horizon = time.Duration(horizon)*time.Second + tail
	cfg.Tick = time.Second
	cfg.Initial.Queue, cfg.Initial.Orbit = from.U, from.V
	for r := 0; r < runs; r++ {
		cfg.Seed = uint64(r + 1)
		m := Simulate(cfg)
		observed, err := trajectorySamples(m, from, tr.Times)
		if err != nil {
			return qmeta.Trajectory{}, fmt.Errorf("qload: trajectory seed %d: %w", r+1, err)
		}
		for i, sample := range observed {
			tr.Mean[i].U += sample.U / float64(runs)
			tr.Mean[i].V += sample.V / float64(runs)
		}
	}
	return tr, nil
}

// trajectorySamples validates each observation's timestamp, not just its index:
// an incomplete or differently sampled run cannot be mistaken for a full one.
func trajectorySamples(m *Metrics, from qmeta.State, times []float64) ([]qmeta.State2, error) {
	if m.Err != nil {
		return nil, fmt.Errorf("simulation could not be judged: %w", m.Err)
	}
	out := make([]qmeta.State2, len(times))
	for i, t := range times {
		if t == 0 {
			out[i] = qmeta.State2{U: float64(from.U), V: float64(from.V)}
			continue
		}
		k := int64(t) - 1 // sample k is taken at (k+1) s
		if k >= int64(len(m.Samples)) || m.Samples[k].T != time.Duration(int64(t))*time.Second {
			return nil, fmt.Errorf("requested observation at %gs is unavailable: the simulation must provide every requested one-second sample", t)
		}
		sample := m.Samples[k]
		out[i] = qmeta.State2{U: float64(sample.Queue), V: float64(sample.Orbit)}
	}
	return out, nil
}
