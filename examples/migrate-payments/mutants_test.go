package payments_test

import (
	"reflect"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// The mutants are bugs of the payment handler, each a directory of
// testdata/mutants whose files replace the package's of the same name (a
// go build overlay): forgetsPriorRefunds is the legacy bug itself, the
// code production ran; the others are bugs the legacy suite would miss
// as well. Each mutant's binary runs TestMutant, which states what
// catches it and what does not: the legacy tests' own assertions pass
// in every one (a legacy run calls the test with this t), and their
// recorded runs conform.
func TestMutants(t *testing.T) {
	qtest.Mutants{}.Run(t)
}

func TestMutant(t *testing.T) {
	if qtest.Mutant() == "" {
		t.Skip("the code as it is: TestMutants runs this test in each mutant's binary")
	}
	qtest.RequireQuint(t)
	// what the legacy suite sees: its assertions pass, its runs conform
	qtest.Runs(legacyRuns(t)...).Expect(t, qtest.All(qtest.Conforms()))
	day := logSteps(t, productionLog)
	spans, _ := converted(t, rulesFile, stagingSpansNow(t))
	switch m := qtest.Mutant(); m {
	case "forgetsPriorRefunds":
		// the production log's day, run again on the legacy code, is the
		// log: the same operations with the same results
		run := recorded(t, replay(day))
		sameRecords(t, run.Steps, day)
		run.Expect(t, qtest.Violates("refundsWithinCapture").At("refund"))
		// staging never refunded a charge twice: its spans miss it
		qtest.Validate(t, bindingFile, spans).Expect(t, qtest.Conforms())
	case "replayCreatesCharge":
		// the log's retry (audit_seq 2) makes a second charge
		recorded(t, replay(day)).Expect(t, qtest.Rejects("authorize"))
		qtest.Validate(t, bindingFile, spans).Expect(t, qtest.Rejects("authorize"))
	case "staleLookup":
		// the log's lookup of ch_3 after its refund (audit_seq 13)
		recorded(t, replay(day)).Expect(t, qtest.Mismatch("lookup"))
		qtest.Validate(t, bindingFile, spans).Expect(t, qtest.Mismatch("lookup"))
	default:
		t.Fatalf("mutant %s: say what catches it", m)
	}
}

// sameRecords fails unless a replay's steps are the log's: each the same
// action with the same arguments, observations and result.
func sameRecords(t *testing.T, got, want []qtrace.Step) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("the replay recorded %d steps, the log has %d", len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Action != w.Action || g.Outcome != w.Outcome || !reflect.DeepEqual(g.Args, w.Args) || !reflect.DeepEqual(g.Obs, w.Obs) {
			t.Errorf("step %d: the replay recorded %s, the log (%s) %s", i+1, g.String(), w.Source, w.String())
		}
	}
}
