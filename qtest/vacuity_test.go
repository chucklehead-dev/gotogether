package qtest

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// An exploration whose runs never pass the gate conforms to
// passedWhenOpen for nothing: its antecedent never held, and NotVacuous
// says so. Runs that pass witness it.
func TestVacuity(t *testing.T) {
	RequireQuint(t)
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
	idle := System{Binding: "testdata/gate.yaml", Run: scenario(false)}.Explore(t, ExploreConfig{Seeds: 3})
	idle.Expect(t, Conforms())
	vs := idle.Vacuous()
	if len(vs) != 1 || vs[0].Invariant != "passedWhenOpen" || !strings.Contains(vs[0].Antecedent, "passes") {
		t.Fatalf("vacuous: %v", vs)
	}
	if e := idle.Evidence().String(); !strings.Contains(e, "vacuity   passedWhenOpen") {
		t.Errorf("evidence:\n%s", e)
	}
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's FailNow ends the goroutine (runtime.Goexit)
		defer close(done)
		idle.Expect(f, NotVacuous())
	}()
	<-done
	if !f.failed || !strings.Contains(strings.Join(f.errs, "\n"), "held vacuously: passedWhenOpen") {
		t.Errorf("NotVacuous on idle runs: %q", f.errs)
	}
	System{Binding: "testdata/gate.yaml", Run: scenario(true)}.Explore(t, ExploreConfig{Seeds: 3}).Expect(t, Conforms(), NotVacuous())
}

// NotVacuous fails when it cannot judge: no invariant has an antecedent,
// or one quint cannot evaluate. Either way it would hold for nothing.
func TestNotVacuousCannotJudge(t *testing.T) {
	RequireQuint(t)
	run := func(c qenv.Chooser) error {
		ctx := qenv.ContextOf(c)
		qobs.Record(ctx, "open", nil)
		qobs.Record(ctx, "pass", nil)
		return nil
	}
	for binding, want := range map[string]string{
		"testdata/gate-plain.yaml":   "no invariant the runs check has an antecedent",
		"testdata/gate-badante.yaml": "the antecedents could not be evaluated",
	} {
		x := System{Binding: binding, Run: run}.Explore(t, ExploreConfig{Seeds: 1})
		f := &failTB{TB: t}
		done := make(chan struct{})
		go func() { //qenv:ok failTB's FailNow ends the goroutine (runtime.Goexit)
			defer close(done)
			x.Expect(f, Conforms(), NotVacuous())
		}()
		<-done
		if !f.failed || !strings.Contains(strings.Join(f.errs, "\n"), want) {
			t.Errorf("%s: %q, want %q", binding, f.errs, want)
		}
	}
}
