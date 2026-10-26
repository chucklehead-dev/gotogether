package qhegel

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A case whose recorder lost its tail, or that starts from a snapshot,
// reuses no prefix's verdict: a failure after a lost tail's cut is no
// verdict, and where a trace starts decides every state after it. A
// whole case is still keyed.
func TestCaseKeysLost(t *testing.T) {
	c, err := validate.NewChecker("../validate/testdata/snapshot.yaml", validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cc := newCaseChecker(c)
	deposit := qtrace.Step{Action: "deposit", Recorder: "r", Seq: 1, Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}
	restore := qtrace.Step{Action: "restore", Recorder: "r", Seq: 1, Args: map[string]any{"b": int64(50), "n": int64(1)}}
	after := deposit
	after.Seq = 2
	for _, x := range []struct {
		name       string
		steps      []qtrace.Step
		prefixable bool
	}{
		{"whole", []qtrace.Step{deposit}, true},
		{"a lost tail", qtrace.MarkLost([]qtrace.Step{deposit}, qtrace.Lost{Recorder: "r", After: 1}), false},
		{"a snapshot start", []qtrace.Step{restore, after}, false},
	} {
		if k := cc.key(x.steps); k.prefixable != x.prefixable {
			t.Errorf("%s: prefixable %v, want %v", x.name, k.prefixable, x.prefixable)
		}
	}
}
