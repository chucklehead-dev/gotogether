package requeue

import (
	"fmt"
	"slices"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// system is the controller and the cloud as one System, judged by the
// faithful model (RESEND false) or, storm, by the model that allows a
// resend to replace the resource. In a mutant's binary it names the
// mutant: its runs are not the controller's.
func system(storm bool) qtest.System {
	b := "requeue.binding.yaml"
	if storm {
		b = "requeue.storm.binding.yaml"
	}
	return qtest.System{Binding: b, Run: Run, Mutant: qtest.Mutant()}
}

// lossy loses, duplicates or delays one message in five, for the five
// seconds before the settle.
var lossy = qenv.Rates{Default: 0.2}

// The faithful model is live: in its whole graph no fair cycle keeps
// reconciled false (a requeue is a stutter, which fairness counts as no
// step: requeueing forever is no cycle), so the model drives it itself.
func TestModelLive(t *testing.T) {
	qtest.Model(t, "requeue.binding.yaml").Expect(t, qtest.Live("reconciled"))
}

func TestStormModelHasLasso(t *testing.T) {
	qtest.Model(t, "requeue.storm.binding.yaml").Expect(t, qtest.Refutes("reconciled"))
}

// Through lost, duplicated and late applies the faithful controller
// conforms, every run settles with the object reconciled (Finishes), and
// every action and fault kind was exercised: a duplicate or late apply
// of the same spec changes nothing.
func TestFaithfulConforms(t *testing.T) {
	qtest.RequireQuint(t)
	system(false).WithRates(lossy).Explore(t, qtest.ExploreConfig{Seeds: 30}).
		Expect(t, qtest.Finishes(), qtest.Exercised())
}

// stormCycle: the run's steps are the model's (an unsettled run does not
// conform: its eventual val is not judged), and its hot cycle, evidence
// beside the verdicts, is the storm's lasso, an advance to warming and a
// replacement back to pending, the controller's requeue among them, gone
// round again and again.
var stormCycle = qtest.Satisfies("the storm's hot cycle (advance, create)", func(r *validate.Report) error {
	for _, a := range r.Actors {
		if a.Conforms != validate.Pass {
			return fmt.Errorf("actor %s: conformance %s", a.Actor, a.Conforms)
		}
		var steps []string
		if a.Hot != nil {
			steps = slices.DeleteFunc(slices.Clone(a.Hot.Steps), func(s string) bool { return s == "requeue" })
		}
		if a.Hot == nil || a.Hot.Repeats < 10 || !slices.Equal(steps, []string{"advance", "create"}) {
			return fmt.Errorf("actor %s: hot cycle %v", a.Actor, a.Hot)
		}
	}
	return nil
})

// TestMutant states what catches the mutant this binary was built with
// (testdata/mutants/<name>: files replacing the package's of the same
// name); TestMutants builds and runs it once per mutant.
func TestMutant(t *testing.T) {
	switch qtest.Mutant() {
	case "":
		t.Skip("the controller as it is: TestMutants runs this test in each mutant's binary")
	case "stampSpec":
		// Every apply's spec carries the reconcile's time, so every
		// requeue replaces the resource: the faithful model has no create
		// but the first, and rejects the second. With no faults the
		// storm is the same in every run.
		qtest.RequireQuint(t)
		system(false).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 3}).
			Expect(t, qtest.Some(qtest.Rejects("create")))
		// The storm's model allows the replacement: the runs conform and
		// never settle, and each report's hot cycle (evidence, no
		// verdict) is the lasso simulate -graph finds in that model, the
		// requeue between a stutter.
		system(true).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 3}).
			Expect(t, qtest.None(qtest.Settles()), qtest.All(stormCycle))
	case "giveUp":
		// The controller gives up after its first requeue, as one with a
		// retry limit tuned to a faster cloud: every step it takes the
		// model allows, the runs settle, and reconciled is false at the
		// settle.
		// With faults a lost response's timeout can delay its second try
		// until the resource is ready: some runs still reconcile.
		qtest.RequireQuint(t)
		reconciled := qtest.Violates(validate.EventuallyName("reconciled"))
		system(false).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 3}).
			Expect(t, qtest.All(reconciled), qtest.Settles())
		system(false).WithRates(lossy).Explore(t, qtest.ExploreConfig{Seeds: 30}).
			Expect(t, qtest.Some(reconciled), qtest.Some(qtest.Finishes()), qtest.Settles())
	default:
		t.Fatalf("mutant %s: say what catches it", qtest.Mutant())
	}
}

// Each mutant of testdata/mutants, built with its overlay, is caught as
// TestMutant says.
func TestMutants(t *testing.T) {
	t.Parallel() // each mutant's binary is a process of its own
	qtest.Mutants{}.Run(t)
}
