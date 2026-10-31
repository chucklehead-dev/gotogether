package qtest

import (
	"fmt"
	"maps"
	"math"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

// resolveValidation snapshots environment-selected policies without turning
// automatic batch backend selection into an explicitly selected Rust backend.
func resolveValidation(c *settings) error {
	if c.linearizeBudget < 0 {
		return fmt.Errorf("LinearizeBudget (%d) must be nonnegative", c.linearizeBudget)
	}
	if c.clockSkew < 0 {
		return fmt.Errorf("ClockSkew (%s) must be nonnegative", c.clockSkew)
	}
	if c.Linearize && c.linearizeBudget == 0 {
		c.linearizeBudget = validate.DefaultLinearizeBudget
	}
	if c.Linearize && c.clockSkew == 0 {
		c.clockSkew = qtrace.RealTimeSlack
	}
	var err error
	c.Options, err = c.Options.ResolveEnvironment()
	return err
}

func validationProfile(c settings) runprofile.Validation {
	backend := c.Options.Backend
	if backend == "" {
		backend = "automatic"
	}
	limit := c.Options.ChoiceLimit
	if limit == 0 {
		limit = validate.DefaultChoiceLimit
	}
	p := runprofile.Validation{Quint: c.Options.Quint, Backend: backend, Eval: c.Options.Eval, ChoiceLimit: limit}
	if c.Options.EvalSample != nil {
		p.ConfirmationRate = c.Options.EvalSample.Rate
		p.ConfirmationSeed = c.Options.EvalSample.Seed
	}
	if c.Linearize || c.resolvePending {
		p.Search = &runprofile.Search{Linearize: c.Linearize, Key: c.linearizeKey, OrderBudget: c.linearizeBudget, ResolvePending: c.resolvePending, ClockSkew: c.clockSkew.String()}
		if c.resolvePending && !c.Linearize {
			p.Search.ResolveBudget = validate.DefaultResolveBudget
		}
	}
	return p
}

// resolvedExplore is both the source plan and its serializable description.
// No environment is read after resolution to decide the seeded sample.
type resolvedExplore struct {
	cfg      exploreSpec
	count    int
	offset   uint64
	full     int
	envSeeds bool
	short    bool
}

func resolveExplore(cfg exploreSpec, short bool) (resolvedExplore, error) {
	r := resolvedExplore{cfg: cfg}
	if cfg.Seeds < 0 || cfg.ShortSeeds < 0 || cfg.Parallel < 0 || cfg.Steer < 0 {
		return r, fmt.Errorf("ExploreConfig counts must be nonnegative: Seeds=%d ShortSeeds=%d Parallel=%d Steer=%d", cfg.Seeds, cfg.ShortSeeds, cfg.Parallel, cfg.Steer)
	}
	if cfg.Seeds == 0 {
		cfg.Seeds = 50
	}
	if cfg.ShortSeeds > cfg.Seeds {
		return r, fmt.Errorf("ExploreConfig.ShortSeeds (%d) exceeds Seeds (%d): lower ShortSeeds", cfg.ShortSeeds, cfg.Seeds)
	}
	if cfg.ShortSeeds > 0 && short {
		r.full = cfg.Seeds
		cfg.Seeds = cfg.ShortSeeds
		r.short = true
	}
	if cfg.Parallel == 0 {
		cfg.Parallel = runtime.GOMAXPROCS(0)
	}
	if cfg.rates.Default == 0 && cfg.rates.Kinds == nil {
		cfg.rates = qenv.DefaultRates
	}
	cfg.without = append([]string{}, cfg.without...)
	cfg.rates.Kinds = maps.Clone(cfg.rates.Kinds)
	if cfg.rates.Kinds == nil {
		cfg.rates.Kinds = map[string]float64{}
	}
	checkRate := func(k string, p float64) error {
		if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
			return fmt.Errorf("System.Rates %s=%v: want a finite probability from 0 through 1", k, p)
		}
		return nil
	}
	if err := checkRate("default", cfg.rates.Default); err != nil {
		return r, err
	}
	for k, p := range cfg.rates.Kinds {
		if k == "" || strings.HasPrefix(k, "fault.") {
			return r, fmt.Errorf("System.Rates key %q: use a nonempty fault kind without the fault. prefix", k)
		}
		if err := checkRate(k, p); err != nil {
			return r, err
		}
	}
	r.count = cfg.Seeds
	n, err := envUint("GOTOGETHER_SEEDS")
	if err != nil {
		return r, err
	}
	_, r.envSeeds = os.LookupEnv("GOTOGETHER_SEEDS")
	offset, err := envUint("GOTOGETHER_SEED_OFFSET")
	if err != nil {
		return r, err
	}
	if cfg.Chooser == nil {
		if n > 0 {
			if n > uint64(math.MaxInt) {
				return r, fmt.Errorf("GOTOGETHER_SEEDS=%d exceeds the supported seed count", n)
			}
			r.count = int(n)
		}
		r.offset = offset
		maxCount := r.count
		if !r.envSeeds && r.full > maxCount {
			maxCount = r.full
		}
		if uint64(maxCount) > math.MaxUint64-offset {
			return r, fmt.Errorf("seed range overflows uint64: offset %d plus %d seeds; lower GOTOGETHER_SEED_OFFSET or GOTOGETHER_SEEDS", offset, maxCount)
		}
	}
	if cfg.Chooser != nil && cfg.Steer > 0 {
		return r, fmt.Errorf("ExploreConfig.Steer with a Chooser: steer seeded explorations so replayed prefixes and fresh choices use the same chooser")
	}
	if cfg.baseline != "" && cfg.Steer > 0 {
		return r, fmt.Errorf("ExploreConfig.Steer in System.Mutate: mutate seeded explorations so every mutant receives the baseline sample")
	}
	r.cfg = cfg
	return r, nil
}

func (r resolvedExplore) profile(x *Exploration) *runprofile.Profile {
	p := x.profile
	p.Mode, p.Chooser = "seeded", "seeded"
	p.Seeds = &runprofile.Seeds{Count: r.count, Offset: r.offset, Short: r.short, Steer: r.cfg.Steer}
	if r.full > r.count && !r.envSeeds && r.cfg.Chooser == nil && !r.cfg.fuzz && len(setKinds(r.cfg.rates)) > 0 {
		p.Seeds.TopUpTo = r.full
	}
	if r.cfg.Chooser != nil {
		p.Mode, p.Chooser = "custom", "custom"
	} else {
		p.Rates = &runprofile.Rates{Default: r.cfg.rates.Default, Kinds: maps.Clone(r.cfg.rates.Kinds)}
	}
	if r.cfg.fuzz {
		p.Mode, p.Chooser = "fuzz", "fuzz-bytes"
		p.Seeds = nil
		p.Rates = &runprofile.Rates{Default: r.cfg.rates.Default, Kinds: maps.Clone(r.cfg.rates.Kinds)}
	}
	p.Corpus = x.corpus
	p.Without = append([]string{}, r.cfg.without...)
	return p
}

func resolveBound(b Bound) (Bound, error) {
	if b.Delays < 0 || b.Faults < 0 || b.MaxRuns < 0 || b.Parallel < 0 {
		return b, fmt.Errorf("Bound counts must be nonnegative: Delays=%d Faults=%d MaxRuns=%d Parallel=%d", b.Delays, b.Faults, b.MaxRuns, b.Parallel)
	}
	if b.MaxRuns == 0 {
		b.MaxRuns = 1000
	}
	if b.Parallel == 0 {
		b.Parallel = runtime.GOMAXPROCS(0)
	}
	for _, k := range b.Without {
		if k == "" || strings.HasPrefix(k, "fault.") {
			return b, fmt.Errorf("Bound.Without key %q: use a nonempty fault kind without the fault. prefix", k)
		}
	}
	b.Without = append([]string{}, b.Without...)
	return b, nil
}

// failConfiguration reports a configuration error before sources can run.
func failConfiguration(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("gotogether: %v", err)
	}
}

// setValidationInstance records the exact primary model instance selected by
// the shared checker construction, including binding defaults and row overrides.
func setValidationInstance(p *runprofile.Profile, c *validate.Checker, constants map[string]string) {
	if p == nil || c == nil {
		return
	}
	p.Validation.Constants = maps.Clone(constants)
	p.Validation.Invariants = append([]string{}, c.Binding.Invariants...)
}
