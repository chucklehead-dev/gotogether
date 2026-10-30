package qtest

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

func cleanProfileEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"GOTOGETHER_SEEDS", "GOTOGETHER_SEED_OFFSET", "QUINT_BACKEND", "GOTOGETHER_EVAL", "GOTOGETHER_EVAL_SAMPLE"} {
		t.Setenv(k, "")
	}
}

func TestResolvedExploreDefaultsAndOverrides(t *testing.T) {
	cleanProfileEnv(t)
	// Empty is still present, but only nonempty counts override. Default short
	// selection and its top-up ceiling use the same resolved source plan.
	r, err := resolveExplore(exploreSpec{ExploreConfig: ExploreConfig{Seeds: 100, ShortSeeds: 5, Parallel: 2}, rates: qenv.DefaultRates.Set("rpc", .5)}, true)
	if err != nil {
		t.Fatal(err)
	}
	if r.count != 5 || r.cfg.Seeds != 5 || r.full != 100 || !r.short || r.cfg.Parallel != 2 {
		t.Fatalf("%+v", r)
	}
	t.Setenv("GOTOGETHER_SEEDS", "20")
	t.Setenv("GOTOGETHER_SEED_OFFSET", "70")
	r, err = resolveExplore(r.cfg, false)
	if err != nil || r.count != 20 || r.offset != 70 {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = resolveExplore(exploreSpec{}, false)
	if err != nil || r.cfg.Seeds != 50 || r.cfg.Parallel < 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestResolvedExploreRejectsInvalidConfigurations(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cfg          exploreSpec
		envName, env string
	}{
		{name: "negative count", cfg: exploreSpec{ExploreConfig: ExploreConfig{Seeds: -1}}},
		{name: "negative parallel", cfg: exploreSpec{ExploreConfig: ExploreConfig{Parallel: -1}}},
		{name: "short exceeds full", cfg: exploreSpec{ExploreConfig: ExploreConfig{Seeds: 1, ShortSeeds: 2}}},
		{name: "nan rate", cfg: exploreSpec{rates: qenv.Rates{Default: math.NaN()}}},
		{name: "infinite rate", cfg: exploreSpec{rates: qenv.Rates{Default: math.Inf(1)}}},
		{name: "invalid probability", cfg: exploreSpec{rates: qenv.NoFaults.Set("rpc", 1.1)}},
		{name: "prefixed rate", cfg: exploreSpec{rates: qenv.Rates{Kinds: map[string]float64{"fault.rpc": .5}}}},
		{name: "overflow", envName: "GOTOGETHER_SEED_OFFSET", env: "18446744073709551615"},
		{name: "oversize count", envName: "GOTOGETHER_SEEDS", env: "18446744073709551615"},
		{name: "bad offset", envName: "GOTOGETHER_SEED_OFFSET", env: "-1"},
		{name: "steered custom", cfg: exploreSpec{ExploreConfig: ExploreConfig{Steer: 1, Chooser: func(uint64) qenv.Chooser { return qenv.Nominal{} }}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanProfileEnv(t)
			if tc.envName != "" {
				t.Setenv(tc.envName, tc.env)
			}
			if _, err := resolveExplore(tc.cfg, false); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestResolvedRatesDoNotAliasSystem(t *testing.T) {
	cleanProfileEnv(t)
	rates := qenv.NoFaults.Set("rpc", .5)
	r, err := resolveExplore(exploreSpec{rates: rates}, false)
	if err != nil {
		t.Fatal(err)
	}
	rates.Kinds["rpc"] = 1
	if r.cfg.rates.Kinds["rpc"] != .5 {
		t.Fatal("execution retained mutable caller rates")
	}
}

func TestResolvedCustomChooserDoesNotClaimRates(t *testing.T) {
	cleanProfileEnv(t)
	t.Setenv("GOTOGETHER_SEEDS", "500")
	t.Setenv("GOTOGETHER_SEED_OFFSET", "1000")
	r, err := resolveExplore(exploreSpec{ExploreConfig: ExploreConfig{Seeds: 2, Chooser: func(uint64) qenv.Chooser { return qenv.Nominal{} }}, rates: qenv.DefaultRates}, false)
	if err != nil || r.count != 2 || r.offset != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	x := &Exploration{profile: &runprofile.Profile{}}
	p := r.profile(x)
	if p.Rates != nil || p.Chooser != "custom" || !strings.Contains(p.String(), "rates unknown") {
		t.Fatalf("%+v", p)
	}
}

func TestResolvedValidationSnapshotsPolicy(t *testing.T) {
	cleanProfileEnv(t)
	c := settings{Linearize: true}
	if err := resolveValidation(&c); err != nil {
		t.Fatal(err)
	}
	if !c.Options.NoEnvBackend || c.Options.Backend != "" || c.Options.Eval != "go" || c.linearizeBudget != validate.DefaultLinearizeBudget {
		t.Fatalf("%+v", c)
	}
	t.Setenv("QUINT_BACKEND", "typescript")
	t.Setenv("GOTOGETHER_EVAL", "shadow")
	if c.Options.Backend != "" || c.Options.Eval != "go" {
		t.Fatal("resolved policy changed")
	}
	p := validationProfile(c)
	if p.Backend != "automatic" || p.Search == nil || p.Search.OrderBudget != validate.DefaultLinearizeBudget {
		t.Fatalf("%+v", p)
	}
	d := settings{}
	if err := resolveValidation(&d); err != nil {
		t.Fatal(err)
	}
	if d.Options.Backend != "typescript" || d.Options.Eval != "shadow" {
		t.Fatalf("%+v", d)
	}
	d = settings{Options: validate.Options{Backend: "typo"}}
	if resolveValidation(&d) == nil {
		t.Fatal("invalid backend accepted")
	}
	d = settings{Options: validate.Options{Eval: "typo"}}
	if resolveValidation(&d) == nil {
		t.Fatal("invalid evaluator accepted")
	}
}

func TestResolvedBoundRejectsNegativeAndCopiesExclusions(t *testing.T) {
	if _, err := resolveBound(Bound{Delays: -1}); err == nil {
		t.Fatal("negative bound accepted")
	}
	original := []string{"rpc"}
	b, err := resolveBound(Bound{Without: original})
	if err != nil {
		t.Fatal(err)
	}
	original[0] = "queue"
	if b.MaxRuns != 1000 || b.Parallel < 1 || b.Without[0] != "rpc" {
		t.Fatalf("%+v", b)
	}
}

func TestTopUpAtLastUint64SeedTerminates(t *testing.T) {
	s := &topUpSource{from: math.MaxUint64, to: math.MaxUint64, batch: 4, kinds: []string{"rpc"}, chooser: func(uint64) qenv.Chooser { return qenv.Nominal{} }}
	runs := []*Run{{Choices: []qenv.Choice{{Kind: "fault.rpc", N: 2, Value: 0}}}}
	plans := s.next(runs)
	if len(plans) != 1 || plans[0].origin.seed != math.MaxUint64 {
		t.Fatalf("%+v", plans)
	}
	if next := s.next(runs); len(next) != 0 {
		t.Fatalf("wrapped source planned %d additional seeds", len(next))
	}
}

func TestResolvedShortTopUpMatchesEffectiveRange(t *testing.T) {
	cleanProfileEnv(t)
	// No override is present, so the short sampler can extend to the full bound.
	if err := os.Unsetenv("GOTOGETHER_SEEDS"); err != nil {
		t.Fatal(err)
	}
	cfg := exploreSpec{ExploreConfig: ExploreConfig{Seeds: 20, ShortSeeds: 2}, rates: qenv.NoFaults.Set("rpc", .2)}
	r, err := resolveExplore(cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	x := &Exploration{profile: &runprofile.Profile{}}
	p := r.profile(x)
	if p.Seeds.Count != 2 || p.Seeds.TopUpTo != 20 {
		t.Fatalf("%+v", p.Seeds)
	}
	t.Setenv("GOTOGETHER_SEED_OFFSET", "18446744073709551613")
	if _, err := resolveExplore(cfg, true); err == nil {
		t.Fatal("top-up range overflow accepted")
	}
}

func TestResolvedFuzzProfileDoesNotClaimSeededSample(t *testing.T) {
	cleanProfileEnv(t)
	r, err := resolveExplore(exploreSpec{ExploreConfig: ExploreConfig{Seeds: 1, Parallel: 1, Chooser: func(uint64) qenv.Chooser { return qenv.FromBytes(nil, qenv.NoFaults) }}, rates: qenv.NoFaults, fuzz: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	x := &Exploration{profile: &runprofile.Profile{}}
	p := r.profile(x)
	if p.Mode != "fuzz" || p.Chooser != "fuzz-bytes" || p.Seeds != nil || p.Rates == nil || strings.Contains(p.String(), "initial sample") {
		t.Fatalf("%+v", p)
	}
}
