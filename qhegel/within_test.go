package qhegel

import (
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A binding with a bounded response keys a case by its steps' times too:
// a slow and a fast run of one sequence of steps are two checks (they
// were one, the slow one passing as the fast), and no prefix's verdict is
// reused. Without one, their keys are one.
func TestCaseKeysWithin(t *testing.T) {
	run := func(elect int) []qtrace.Step {
		at := func(s int) time.Time { return time.Unix(0, 0).Add(time.Duration(s) * time.Second) }
		return []qtrace.Step{{Action: "crash", Recorder: "n", Seq: 1, Time: at(0)}, {Action: "heal", Recorder: "n", Seq: 2, Time: at(1)},
			{Action: "elect", Recorder: "n", Seq: 3, Time: at(elect)}}
	}
	for _, x := range []struct {
		binding string
		apart   bool
	}{{"../validate/testdata/within.yaml", true}, {"../validate/testdata/blind.yaml", false}} {
		c, err := validate.NewChecker(x.binding, validate.Options{})
		if err != nil {
			t.Fatal(err)
		}
		cc := newCaseChecker(c)
		fast, slow := cc.key(run(2)), cc.key(run(5))
		full := func(k keyed) string { return k.keys[len(k.keys)-1] }
		if apart := full(fast) != full(slow); apart != x.apart {
			t.Errorf("%s: keys apart %v, want %v", x.binding, apart, x.apart)
		}
		if x.apart && (fast.prefixable || slow.prefixable) {
			t.Errorf("%s: prefixes reused for a bounded response", x.binding)
		}
		if runKey(c, run(2)) != runKey(c, run(5)) != x.apart {
			t.Errorf("%s: a round's keys apart %v, want %v", x.binding, !x.apart, x.apart)
		}
	}
}
