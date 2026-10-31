package validate

import (
	"fmt"
	"math"
	"os"
	"strings"
)

// EvaluationSampling selects the fraction of evaluator passes confirmed by
// Quint, deterministically hashed with Seed. Rate zero disables sampling.
type EvaluationSampling struct {
	Rate float64
	Seed string
}

// ResolveEnvironment snapshots environment-selected validation policy. The
// returned options can be reused without reading backend, evaluator or sampling
// environment variables again. A blank Backend retains automatic batch/backend
// selection; it does not force Rust for every batch. Invalid policies fail here,
// before execution. The finite model-choice limit is normalized to its effective
// default too. Filesystem paths are not created or resolved by this method.
func (o Options) ResolveEnvironment() (Options, error) {
	if o.ChoiceLimit < 0 {
		return o, fmt.Errorf("choice limit must be nonnegative (0 uses the default)")
	}
	if o.ChoiceLimit == 0 {
		o.ChoiceLimit = DefaultChoiceLimit
	}
	o.Backend = o.envBackend()
	o.NoEnvBackend = true
	if o.Backend != "" && o.Backend != "rust" && o.Backend != "typescript" {
		return o, fmt.Errorf("backend %q: want rust or typescript; clear QUINT_BACKEND to select automatically", o.Backend)
	}
	if o.Eval == "" {
		o.Eval = os.Getenv("GOTOGETHER_EVAL")
	}
	if o.Eval == "" {
		o.Eval = "go"
	}
	if o.Eval != "go" && o.Eval != "quint" && o.Eval != "shadow" {
		return o, fmt.Errorf("evaluator %q: want go, quint or shadow (GOTOGETHER_EVAL)", o.Eval)
	}
	if o.Quint == "" {
		o.Quint = "quint"
	}
	if o.EvalSample == nil {
		s := EvaluationSampling{Rate: defaultSample}
		if v := strings.TrimSpace(os.Getenv("GOTOGETHER_EVAL_SAMPLE")); v != "" {
			rate, seed, _ := strings.Cut(v, ":")
			f, ok := parseRate(rate)
			if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
				return o, fmt.Errorf("GOTOGETHER_EVAL_SAMPLE=%q: want a probability from 0 through 1, optionally followed by :seed", v)
			}
			s.Rate, s.Seed = f, seed
		}
		o.EvalSample = &s
	} else {
		s := *o.EvalSample
		o.EvalSample = &s
	}
	if f := o.EvalSample.Rate; math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1 {
		return o, fmt.Errorf("EvalSample.Rate=%v: want a finite probability from 0 through 1", f)
	}
	return o, nil
}
