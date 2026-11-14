package qtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
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

// The vacuity check by a compiled model alone: the model carries every
// candidate invariant's antecedent witness (validate.AntecedentWitnesses),
// so with GOTOGETHER_COMPILED=1 idle runs find passedWhenOpen vacuous and
// NotVacuous fails, as with quint, and runs that pass make it hold. An
// invariant the binding does not declare (neverNegative) is checked by the
// compiled model too (Invariants). It once refused, "needs quint". Not
// parallel: it sets the environment.
func TestVacuityByCompiledModel(t *testing.T) {
	RequireQuint(t) // to compile
	dir := t.TempDir()
	for _, name := range []string{"gate.yaml", "gate.qnt"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b := filepath.Join(dir, "gate.yaml")
	artifact, err := validate.Compile(b, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := artifact.Write(validate.CompiledFor(b)); err != nil {
		t.Fatal(err)
	}
	t.Setenv(compiledEnv, "1")
	scenario := func(pass bool) func(qenv.Chooser) error {
		return func(c qenv.Chooser) error {
			ctx := qenv.ContextOf(c)
			qobs.Record(ctx, "open", nil)
			if pass {
				qobs.Record(ctx, "pass", nil)
			}
			return nil
		}
	}
	idle := System{Binding: b, Run: scenario(false)}.Explore(t, ExploreConfig{Seeds: 3})
	if idle.checker.Compiled() == nil {
		t.Fatal("the exploration was not judged by the compiled model")
	}
	idle.Expect(t, Conforms())
	r := idle.vacuous()
	if r.err != nil || len(r.found) != 1 || r.found[0].Invariant != "passedWhenOpen" {
		t.Fatalf("vacuity by the compiled model: %+v", r)
	}
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's FailNow ends the goroutine (runtime.Goexit)
		defer close(done)
		idle.Expect(f, NotVacuous())
	}()
	<-done
	if !f.failed || !strings.Contains(strings.Join(f.errs, "\n"), "held vacuously: passedWhenOpen") {
		t.Errorf("NotVacuous on idle runs, compiled: %q", f.errs)
	}
	System{Binding: b, Run: scenario(true)}.Explore(t, ExploreConfig{Seeds: 3}).Expect(t, Conforms(), NotVacuous())
	// an invariant outside the binding's, by the compiled model
	Validate(t, b, []qtrace.Step{{Action: "open", Seq: 1}, {Action: "pass", Seq: 2}}, Invariants("neverNegative")).Expect(t, Conforms())
}
