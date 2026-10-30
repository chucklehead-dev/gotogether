package validate

import (
	"math"
	"testing"
)

func TestResolvedEnvironmentRemainsAutomaticAndStable(t *testing.T) {
	t.Setenv("QUINT_BACKEND", "")
	t.Setenv("GOTOGETHER_EVAL", "")
	t.Setenv("GOTOGETHER_EVAL_SAMPLE", "1/5:nightly")
	o, err := (Options{}).ResolveEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUINT_BACKEND", "typescript")
	t.Setenv("GOTOGETHER_EVAL", "quint")
	t.Setenv("GOTOGETHER_EVAL_SAMPLE", "0")
	if o.envBackend() != "" || !o.NoEnvBackend || o.evalMode() != "go" || o.EvalSample.Rate != .2 || o.EvalSample.Seed != "nightly" {
		t.Fatalf("resolved policy changed: %+v", o)
	}
	again, err := o.ResolveEnvironment()
	if err != nil || again.envBackend() != "" || again.EvalSample.Rate != .2 {
		t.Fatalf("second resolution: %+v, %v", again, err)
	}
	again.EvalSample.Rate = 0
	if o.EvalSample.Rate != .2 {
		t.Fatal("resolved options share mutable sampling")
	}
}

func TestResolvedEnvironmentRejectsMalformedPolicies(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"QUINT_BACKEND", "typo"}, {"GOTOGETHER_EVAL", "typo"},
		{"GOTOGETHER_EVAL_SAMPLE", "oops"}, {"GOTOGETHER_EVAL_SAMPLE", "NaN"},
		{"GOTOGETHER_EVAL_SAMPLE", "1/0"}, {"GOTOGETHER_EVAL_SAMPLE", "1.1"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			for _, key := range []string{"QUINT_BACKEND", "GOTOGETHER_EVAL", "GOTOGETHER_EVAL_SAMPLE"} {
				t.Setenv(key, "")
			}
			t.Setenv(tc.key, tc.value)
			if _, err := (Options{}).ResolveEnvironment(); err == nil {
				t.Fatal("malformed policy accepted")
			}
		})
	}
	for _, f := range []float64{math.NaN(), math.Inf(1), -.1, 1.1} {
		if _, err := (Options{EvalSample: &EvaluationSampling{Rate: f}}).ResolveEnvironment(); err == nil {
			t.Fatalf("invalid rate %v accepted", f)
		}
	}
}
