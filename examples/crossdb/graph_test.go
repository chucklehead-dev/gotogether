package crossdb_test

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The model's whole state graph (simulate -graph): oneDatabasePerResource
// violated two steps from init, as the binding allows; the 128 states
// the step cannot leave are those with the step's three database ids
// spent, the abstraction's end (simulate.terminal), and no others.
func TestGraphEndsWhenIdsSpent(t *testing.T) {
	qtest.RequireQuint(t)
	c, err := validate.NewChecker(binding, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, err := c.Graph(validate.GraphSearch{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Complete || r.States != 10377 {
		t.Fatalf("graph %s", r.Extent())
	}
	if dup := r.Invariants[0]; dup.Name != "oneDatabasePerResource" || !dup.Violated || len(dup.Counterexample.Steps) != 3 {
		t.Fatalf("oneDatabasePerResource %+v", dup)
	}
	if r.Deadlocks != 0 || r.Terminals != 128 {
		t.Fatalf("deadlocks %d (nearest %+v), terminals %d", r.Deadlocks, r.Deadlock, r.Terminals)
	}
}
