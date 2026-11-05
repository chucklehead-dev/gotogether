package counters

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
)

// The model on its own proves its invariants within a bound: a grist
// model's log grows without end, so its state graph is never complete,
// and a state bound alone would be a breadth-first prefix proving
// nothing. A test-local depth bound makes it every state within three
// commands of init, a bounded proof the claim says so, without changing
// the generated binding or copying its model tree.
func TestModelProvesWithinDepth(t *testing.T) {
	qtest.Model(t, "counters.binding.yaml").WithOptions(qtest.ModelOptions{Depth: 3}).
		Expect(t, qtest.Proves("uniqueNames"), qtest.Proves("averageAsOfTrigger"))
}
