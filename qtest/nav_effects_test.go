package qtest

import (
	"reflect"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A navigation node lists steps only: each effect record under the step
// qtrace attributes it to (pay's call: its captures and notice), one made
// by no step apart, and none counted as a step. An invocation record is
// listed as before, without its completion's effects.
func TestNodeStepLinesAttributeEffects(t *testing.T) {
	t.Parallel()
	b, err := binding.Load("testdata/pay.yaml")
	if err != nil {
		t.Fatal(err)
	}
	steps := payLog(20, "cap:o1", true)
	// an effect of the shop's no step made: a send after pay completed,
	// under no call, by a task that records nothing after it
	stray := steps[2]
	stray.Seq, stray.Call, stray.Time = 7, 0, steps[len(steps)-1].Time.Add(1)
	steps = append(steps, stray)

	lines, effects, unattributed := stepLines(b, steps)
	if want := []string{"place(order=o1, qty=2)", "pay(order=o1)", "pay(order=o1)"}; !reflect.DeepEqual(lines, want) {
		t.Errorf("lines %q, want %q", lines, want)
	}
	want := [][]string{nil, nil, {
		"rpc.provider.request(Amount=20, Key=cap:o1, Op=capture, Order=o1) at seq 3",
		"rpc.provider.request(Amount=20, Key=cap:o1, Op=capture, Order=o1) at seq 4",
		"queue.mail.publish(Order=o1) at seq 5",
	}}
	if !reflect.DeepEqual(effects, want) {
		t.Errorf("effects %q, want %q", effects, want)
	}
	if want := []string{"rpc.provider.request(Amount=20, Key=cap:o1, Op=capture, Order=o1) at seq 7"}; !reflect.DeepEqual(unattributed, want) {
		t.Errorf("unattributed %q, want %q", unattributed, want)
	}

	// a node with no effect attributed carries none
	if _, effects, _ := stepLines(b, []qtrace.Step{steps[0]}); effects != nil {
		t.Errorf("effects %q of a node without any", effects)
	}
}
