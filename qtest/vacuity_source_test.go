package qtest

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Witness predicates are generated diagnostics, but still depend on the same
// source as the original judgment. A replay that edits that source must give no
// witness verdict; the original checker must remain usable after restoration.
func TestVacuityProbeGuardsOriginalSource(t *testing.T) {
	RequireQuint(t)
	t.Setenv("GOTOGETHER_CHECK", "1")
	real, err := exec.LookPath("quint")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var original []byte
	for _, name := range []string{"gate.yaml", "gate.qnt"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "gate.qnt" {
			original = data
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	x := System{Binding: filepath.Join(dir, "gate.yaml"), Run: func(c qenv.Chooser) error {
		ctx := qenv.ContextOf(c)
		qobs.Record(ctx, "open", nil)
		qobs.Record(ctx, "pass", nil)
		return nil
	}}.Explore(t, ExploreConfig{Seeds: 1})
	spec := filepath.Join(dir, "gate.qnt")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\n" + quote(real) + " \"$@\"\nstatus=$?\nif [ \"$1\" = test ]; then printf '\\n// changed during witness replay\\n' >> " + quote(spec) + "; fi\nexit \"$status\"\n"
	wrapper := filepath.Join(dir, "quint")
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	x.checker.Options.Quint = wrapper
	verdict := x.findVacuous()
	if verdict.err == nil || !strings.Contains(verdict.err.Error(), "changed") || len(verdict.found) != 0 {
		t.Fatalf("changed source received witness verdict: %+v", verdict)
	}
	if err := os.WriteFile(spec, original, 0o644); err != nil {
		t.Fatal(err)
	}
	x.checker.Options.Quint = real
	if verdict := x.findVacuous(); verdict.err != nil || len(verdict.found) != 0 {
		t.Fatalf("restored source with witnessed antecedent: %+v", verdict)
	}
	if err := x.checker.VerifySource(); err != nil {
		t.Fatalf("diagnostic probe mutated the original checker: %v", err)
	}
}

func TestVacuityProbeCompiledNeedsSource(t *testing.T) {
	RequireQuint(t)
	artifact, err := validate.Compile("testdata/gate.yaml", validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "gate.model.json")
	if err := artifact.Write(path); err != nil {
		t.Fatal(err)
	}
	checker, err := validate.NewCompiledChecker("testdata/gate.yaml", path, validate.Options{Quint: filepath.Join(t.TempDir(), "no-quint")})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	x := &Exploration{checker: checker}
	if result := x.findVacuous(); !errors.Is(result.err, validate.ErrNeedsQuint) {
		t.Fatalf("derived artifact predicate silently fell back to source: %+v", result)
	}
}
