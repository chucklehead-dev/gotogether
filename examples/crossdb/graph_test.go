package crossdb_test

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
)

// The model's whole state graph (simulate -graph): oneDatabasePerResource
// refuted two steps from init, as the binding allows; the states the step
// cannot leave are those with the step's three database ids spent, the
// abstraction's end (simulate.terminal), and no others.
func TestGraphEndsWhenIdsSpent(t *testing.T) {
	qtest.Model(t, binding).Expect(t, qtest.Refutes("oneDatabasePerResource").InAtMost(2), qtest.NoDeadlocks())
}
