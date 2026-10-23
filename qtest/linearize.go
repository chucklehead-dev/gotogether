package qtest

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// DefaultLinearizeBudget is how many orders Linearize checks per actor at
// most, unless LinearizeBudget says otherwise.
const DefaultLinearizeBudget = 200

// Linearize makes Validate and Record check steps in an order their
// real-time intervals allow, searched for one the model accepts
// (qtrace.Linearize), rather than in the order they were recorded. It is
// for steps recorded where an operation was invoked and returned (a
// client's, with "invoked": qobs.Record's key, or converted spans, whose
// start is their invocation): concurrent operations then complete in
// another order than they took effect, and the recording order rejects
// correct code. A step recorded where it takes effect needs none of it.
//
// The run's Steps become the order checked. The report gains an issue
// per actor: "info" with the number of orders tried when one conformed,
// "warn" when none did, the order shown then being the one that got
// furthest. A search that stopped there, no move getting further, is
// strong evidence of a bug: the actor fails as that order does. One that
// ran out of its budget (DefaultLinearizeBudget orders, LinearizeBudget)
// first is no verdict: the actor's conformance is n/a, which Conforms
// fails and Rejects and Mismatch do not match.
func Linearize() Option { return func(c *settings) { c.Linearize = true } }

// LinearizeBudget is Linearize with a budget of n orders per actor (at
// least 1): a run whose search ran out of DefaultLinearizeBudget's says
// to raise it.
func LinearizeBudget(n int) Option {
	if n < 1 {
		panic(fmt.Sprintf("qtest: LinearizeBudget(%d): the budget is a number of orders to check, at least 1", n))
	}
	return func(c *settings) { c.Linearize, c.linearizeBudget = true, n }
}

// linearize orders each actor's steps by the search, checking candidate
// orders with ch in batches, at most budget per actor. undecided are the
// actors whose search ran out of it.
func linearize(t testing.TB, ch *validate.Checker, steps []qtrace.Step, budget int) (out []qtrace.Step, issues []qtrace.Issue, undecided map[string]bool) {
	t.Helper()
	byActor := map[string][]qtrace.Step{}
	for _, s := range steps {
		byActor[s.Actor] = append(byActor[s.Actor], s) // as the report names it ("" for no actor)
	}
	actors := make([]string, 0, len(byActor))
	for a := range byActor {
		actors = append(actors, a)
	}
	sort.Strings(actors)
	undecided = map[string]bool{}
	for _, actor := range actors {
		a := actor // as messages name it
		if a == "" {
			a = "default"
		}
		check := func(orders [][]qtrace.Step) []int {
			reps, errs := ch.CheckBatch(context.Background(), orders, 0)
			fs := make([]int, len(orders))
			for i := range orders {
				if errs[i] != nil {
					t.Fatalf("gotogether: linearizing actor %s: %v", a, errs[i])
				}
				fs[i] = failedAt(reps[i], len(orders[i]))
			}
			return fs
		}
		o, ok, tried := qtrace.Linearize(byActor[actor], budget, check)
		switch {
		case ok:
			issues = append(issues, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: fmt.Sprintf("actor %s: an order of its %d steps conforms (%d orders tried)", a, len(o), tried)})
		case tried >= budget:
			undecided[actor] = true
			issues = append(issues, qtrace.Issue{Severity: "warn", Kind: "linearize", Msg: fmt.Sprintf("actor %s: no order of its %d steps conformed in the %d orders of the budget, and the search had moves left: "+
				"no verdict (conformance n/a). Raise the budget (qtest.LinearizeBudget(%d)), or record its steps where they take effect", a, len(o), tried, 4*budget)})
		default:
			issues = append(issues, qtrace.Issue{Severity: "warn", Kind: "linearize", Msg: fmt.Sprintf("actor %s: no order of its %d steps that real time allows conformed in %d orders tried; "+
				"the order shown got furthest. A step the model rejects in every order is a bug (a stale read, a lost write), unless the invocation times are wrong", a, len(o), tried)})
		}
		out = append(out, o...)
	}
	return out, issues, undecided
}

// failedAt is the index of the first step of a one-actor order the model
// rejects, or -1 when it conforms (an invariant it violates is not the
// order's fault).
func failedAt(rep *validate.Report, n int) int {
	for _, a := range rep.Actors {
		if a.Conforms == validate.Pass {
			continue
		}
		if a.Failure != nil && a.Failure.Step != nil && a.Failure.Step.Seq >= 1 {
			return int(a.Failure.Step.Seq) - 1 // the order's position: re-sequenced from 1
		}
		return n - 1
	}
	return -1
}
