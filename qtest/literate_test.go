package qtest

import (
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A recorded run is checked against a literate model as against any other
// (ROADMAP CW1): the binding's spec names the Markdown document, and the
// steps are judged by the model its quint blocks make.
func TestValidateAgainstALiterateModel(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(s int) time.Time { return time.Unix(1_800_000_000, 0).Add(time.Duration(s) * time.Second) }
	incs := func(n int) []qtrace.Step {
		var steps []qtrace.Step
		for i := range n {
			steps = append(steps, qtrace.Step{Action: "inc", Recorder: "c", Seq: uint64(i + 1), Time: at(i)})
		}
		return steps
	}
	bind := "../validate/testdata/literate/counter.binding.yaml"
	Validate(t, bind, incs(2)).Expect(t, Conforms())
	// the document's deliberately broken promise: a third increment reaches
	// MAX, and the report names the line belowMax is written on
	third := Validate(t, bind, incs(3))
	third.Expect(t, Violates("belowMax"))
	if !strings.Contains(third.Report.Summary(), "literate/counter.md:23): FAIL") {
		t.Errorf("the failing invariant does not name its document line:\n%s", third.Report.Summary())
	}
	// MAX is 3: a fourth increment is one the model refuses
	Validate(t, bind, incs(4)).Expect(t, Rejects("inc"))
}
