package qhegel

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// An eventual property's failure rejects no prefix: [deposit, withdraw]
// ends with the account empty, but [deposit, withdraw, deposit] does not,
// and once was "a prefix the model already rejected" from the cache.
func TestEventualFailureRejectsNoPrefix(t *testing.T) {
	steps := []qtrace.Step{{Action: "deposit", Recorder: "r", Seq: 1}, {Action: "withdraw", Recorder: "r", Seq: 2}}
	ev := &validate.Result{Conforms: validate.Pass, Invariants: []validate.InvariantResult{
		{Name: validate.EventuallyName("funded"), Verdict: validate.Fail, Failure: &validate.Failure{Index: 1, Step: &steps[1]}}}}
	if at := failingStep(ev, steps); at != -1 {
		t.Errorf("an eventual failure marks step %d as the failing prefix's end", at)
	}
	inv := &validate.Result{Conforms: validate.Pass, Invariants: []validate.InvariantResult{
		{Name: "nonNegative", Verdict: validate.Fail, Failure: &validate.Failure{Index: 1, Step: &steps[1]}}}}
	if at := failingStep(inv, steps); at != 1 {
		t.Errorf("an invariant's failure after step 1 marks %d", at)
	}
}
