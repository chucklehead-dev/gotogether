package binding

import (
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A parameter whose field the step lacks says which field, what the step
// has, and the args: fix, not text/template's "map has no entry for key".
func TestMissingFieldMessage(t *testing.T) {
	skip.Quint(t)
	m, err := LoadModel("quint", "testdata/procarg.qnt", "procarg")
	if err != nil {
		t.Fatal(err)
	}
	tr := &qtrace.Trace{Actor: "a", Processes: []string{"p"}, Steps: []qtrace.Step{
		{Action: "crash", Process: "p", Outcome: "ok", Seq: 1, Source: "line 1", Args: map[string]any{"node": "b"}},
	}}
	_, err = (&Binding{Spec: "testdata/procarg.qnt", Module: "procarg"}).Plan(m, tr)
	for _, want := range []string{`step 1 (crash, line 1): parameter process`, `reads the step's field "process"`, "it has: node", "args: { process: '{{.FIELD}}' }"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in %v", want, err)
		}
	}
	if err != nil && strings.Contains(err.Error(), "no entry for key") {
		t.Errorf("template text in %v", err)
	}
}
