package qload

import (
	"errors"
	"math"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qmeta"
)

func trajectoryConfig() SimConfig {
	return SimConfig{
		Server: ServerConfig{Name: "server", Workers: 1, QueueCap: 8, ServiceRate: 3},
		Client: ClientConfig{Name: "client", Rate: Constant(2), Timeout: 2 * time.Second, Retries: 1},
	}
}

// The trajectory must be the same per-time arithmetic mean as independent
// runs, preserving requested ordering and the exact pre-event initial state.
func TestSimulateTrajectoryMatchesIndependentRuns(t *testing.T) {
	cfg := trajectoryConfig()
	cfg.Seed, cfg.Horizon, cfg.Tick = 99, time.Hour, time.Millisecond
	cfg.Initial.Queue, cfg.Initial.Orbit = 7, 5
	from := qmeta.State{U: 2, V: 3}
	times := []float64{3, 0, 1, 3, 2}
	const runs = 4
	tr, err := SimulateTrajectory(cfg, from, times, runs)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]qmeta.State2, len(times))
	for seed := 1; seed <= runs; seed++ {
		run := cfg
		run.Seed = uint64(seed)
		run.Tick, run.Horizon = time.Second, 3*time.Second+cfg.Client.Timeout+time.Second
		run.Initial.Queue, run.Initial.Orbit = from.U, from.V
		metrics := Simulate(run)
		if metrics.Err != nil {
			t.Fatal(metrics.Err)
		}
		for i, when := range times {
			state := qmeta.State2{U: float64(from.U), V: float64(from.V)}
			if when != 0 {
				sample := metrics.Samples[int(when)-1]
				if sample.T != time.Duration(when)*time.Second {
					t.Fatal("independent run did not sample requested time")
				}
				state = qmeta.State2{U: float64(sample.Queue), V: float64(sample.Orbit)}
			}
			want[i].U += state.U / runs
			want[i].V += state.V / runs
		}
	}
	if tr.From != from || !reflect.DeepEqual(tr.Times, times) || !reflect.DeepEqual(tr.Mean, want) {
		t.Fatalf("trajectory %+v, want from %v, times %v, mean %v", tr, from, times, want)
	}
	if tr.Mean[1] != (qmeta.State2{U: 2, V: 3}) {
		t.Fatal("zero-time observation did not preserve initial state")
	}
	times[0] = 123
	if tr.Times[0] != 3 {
		t.Fatal("trajectory aliases caller's times")
	}
	if cfg.Seed != 99 || cfg.Horizon != time.Hour || cfg.Tick != time.Millisecond || cfg.Initial.Queue != 7 || cfg.Initial.Orbit != 5 {
		t.Fatal("trajectory modified caller's configuration")
	}
}

// Orbit is retrospective: an attempt still waiting at the observation only
// enters the orbit once its timeout is known. Stopping just after the final
// sample silently reported zero orbit for this waiting request.
func TestSimulateTrajectoryCompletesOrbitObservations(t *testing.T) {
	cfg := SimConfig{
		Server: ServerConfig{Name: "server", QueueCap: 8, Service: func(*rand.Rand) time.Duration { return 100 * time.Second }},
		Client: ClientConfig{Name: "client", Population: 1, Timeout: 9 * time.Second},
	}
	tr, err := SimulateTrajectory(cfg, qmeta.State{}, []float64{1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Mean[0] != (qmeta.State2{U: 1, V: 1}) {
		t.Fatalf("unfinished orbit observation: %v, want one queued request in orbit", tr.Mean)
	}
	cfg.Seed, cfg.Tick, cfg.Horizon = 1, time.Second, 30*time.Second
	later := Simulate(cfg)
	if later.Err != nil || later.Samples[0].Queue != 1 || later.Samples[0].Orbit != 1 {
		t.Fatalf("longer-run observation disagrees: %+v, %v", later.Samples[0], later.Err)
	}
	cfg.Horizon = 2 * time.Second
	censored := Simulate(cfg)
	if censored.Err != nil || censored.Samples[0].Orbit != 0 {
		t.Fatal("fixture did not demonstrate retrospectively completed orbit")
	}
}

func TestSimulateTrajectoryRejectsInvalidObservations(t *testing.T) {
	base := trajectoryConfig()
	for _, c := range []struct {
		name  string
		from  qmeta.State
		times []float64
		runs  int
		edit  func(*SimConfig)
		want  string
	}{
		{name: "zero_runs", times: []float64{1}, want: "runs must be positive"},
		{name: "negative_runs", times: []float64{1}, runs: -1, want: "runs must be positive"},
		{name: "empty_times", runs: 1, want: "times are empty"},
		{name: "negative_time", times: []float64{-1}, runs: 1, want: "time[0]"},
		{name: "fractional_time", times: []float64{.5}, runs: 1, want: "whole second"},
		{name: "nan_time", times: []float64{math.NaN()}, runs: 1, want: "finite"},
		{name: "infinite_time", times: []float64{math.Inf(1)}, runs: 1, want: "finite"},
		{name: "duration_overflow", times: []float64{float64(math.MaxInt64)}, runs: 1, want: "whole second"},
		{name: "timeout_overflow", times: []float64{0}, runs: 1, edit: func(c *SimConfig) { c.Client.Timeout = math.MaxInt64 }, want: "overflows"},
		{name: "horizon_plus_tail_overflow", times: []float64{float64(math.MaxInt64 / int64(time.Second))}, runs: 1, want: "whole second"},
		{name: "zero_timeout", times: []float64{0}, runs: 1, edit: func(c *SimConfig) { c.Client.Timeout = 0 }, want: "Timeout must be positive"},
		{name: "negative_timeout", times: []float64{0}, runs: 1, edit: func(c *SimConfig) { c.Client.Timeout = -1 }, want: "Timeout must be positive"},
		{name: "negative_queue", from: qmeta.State{U: -1}, times: []float64{0}, runs: 1, want: "initial state"},
		{name: "negative_orbit", from: qmeta.State{V: -1}, times: []float64{0}, runs: 1, want: "initial state"},
		{name: "queue_clipped", from: qmeta.State{U: 9}, times: []float64{0}, runs: 1, want: "capacity 8"},
		{name: "negative_capacity", times: []float64{0}, runs: 1, edit: func(c *SimConfig) { c.Server.QueueCap = -1 }, want: "capacity -1"},
		{name: "default_capacity_clipped", from: qmeta.State{U: 101}, times: []float64{0}, runs: 1, edit: func(c *SimConfig) { c.Server.QueueCap = 0 }, want: "capacity 100"},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := base
			if c.edit != nil {
				c.edit(&cfg)
			}
			tr, err := SimulateTrajectory(cfg, c.from, c.times, c.runs)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v; want %q", err, c.want)
			}
			if !reflect.DeepEqual(tr, qmeta.Trajectory{}) {
				t.Fatalf("invalid trajectory returned partial data: %+v", tr)
			}
		})
	}
}

// Simulate sets MaxSteps to MaxInt and always runs a sampler, so its error and
// missing-sample paths cannot be forced safely through the public configuration.
// Exercise the production observation gate directly without a runner seam.
func TestTrajectorySamplesRequireCompleteEvidence(t *testing.T) {
	failure := errors.New("simulation stopped early")
	for _, c := range []struct {
		name string
		m    *Metrics
		want string
	}{
		{"failed", &Metrics{Err: failure, Samples: []Sample{{T: time.Second}}}, "could not be judged"},
		{"missing", &Metrics{}, "1s is unavailable"},
		{"wrong_timestamp", &Metrics{Samples: []Sample{{T: 2 * time.Second}}}, "1s is unavailable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			data, err := trajectorySamples(c.m, qmeta.State{U: 2, V: 3}, []float64{0, 1})
			if err == nil || !strings.Contains(err.Error(), c.want) || data != nil {
				t.Fatalf("observations %v, error %v; want no data and %q", data, err, c.want)
			}
			if c.m.Err != nil && !errors.Is(err, failure) {
				t.Fatalf("simulation error not retained: %v", err)
			}
		})
	}
}
