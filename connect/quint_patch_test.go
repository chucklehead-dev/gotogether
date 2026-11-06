package connect

import (
	"fmt"
	"testing"
)

// Quint 0.33.0's TypeScript simulator recorded a dead-end sample twice.
// With n-traces=max-samples, duplicate long runs displaced sampled short runs.
func TestGeneratedTerminalSamplesRetainShortRuns(t *testing.T) {
	needQuint(t)
	cfg := Config{Spec: "testdata/terminal_samples.qnt", Backend: "typescript", Seed: "42", MaxSamples: 5, MaxSteps: 5, Dir: t.TempDir()}
	files, cfg, err := GenerateRun(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	traces, err := ReadTraces(t.Context(), cfg, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 5 {
		t.Fatalf("generated %d traces; want all 5 samples", len(traces))
	}
	short, long := 0, 0
	for _, tr := range traces {
		initial := tr.States[0].Vars["bound"].Int64()
		final := tr.States[len(tr.States)-1].Vars["n"].Int64()
		if final != initial || len(tr.States) != int(initial)+1 {
			t.Fatalf("incomplete terminal sample: bound=%d final=%d states=%d", initial, final, len(tr.States))
		}
		if initial == 0 {
			short++
		}
		if initial == 4 {
			long++
		}
	}
	if short == 0 || long == 0 {
		t.Fatalf("seed 42 must retain both sampled initial-terminal and long runs: short=%d long=%d", short, long)
	}
}

type constructorNameDriver struct{ count int }

func (d *constructorNameDriver) Step(s *Step) error {
	switch s.Action {
	case "init":
		d.count = 0
	case "step":
		d.count++
	default:
		return fmt.Errorf("unexpected constructor-name action %q", s.Action)
	}
	return nil
}
func (d *constructorNameDriver) State() (any, error) {
	return struct {
		ConstructorCount int `itf:"constructorCount"`
	}{d.count}, nil
}

// The paired Rust evaluator's JSON may use a constructor substring as a key.
// Verify real generation, decoding and state comparison, not just CLI startup.
func TestGeneratedRustConstructorNames(t *testing.T) {
	needQuint(t)
	cfg := Config{Spec: "testdata/constructor_names.qnt", Backend: "rust", Seed: "42", MaxSamples: 3, MaxSteps: 5, Dir: t.TempDir()}
	files, cfg, err := GenerateRun(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	traces, err := ReadTraces(t.Context(), cfg, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 3 {
		t.Fatalf("generated %d traces; want 3", len(traces))
	}
	for i, tr := range traces {
		d := &constructorNameDriver{}
		if err := Replay(tr, d, ReplayOptions{Trace: i}); err != nil {
			t.Fatal(err)
		}
		if d.count != 2 {
			t.Fatalf("constructor-name sample stopped at %d; want 2", d.count)
		}
	}
}
