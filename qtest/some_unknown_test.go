package qtest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate"
)

func someReport(outcome string) *validate.Report {
	return &validate.Report{Actors: []*validate.Result{{Actor: outcome, Conforms: validate.Pass}}}
}

func someExpectation() Expectation {
	return Satisfies("complete decision evidence", func(r *validate.Report) error {
		switch r.Actors[0].Actor {
		case "witness":
			return nil
		case "unknown":
			return fmt.Errorf("%w: regenerate the application model", ErrNoVerdict)
		default:
			return fmt.Errorf("decision was observed and did not match")
		}
	})
}

func TestSomeRetainsUnknownWithoutDiscardingWitness(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                string
		runs                []string
		ok                  bool
		witnesses, unjudged int
	}{
		{"only unknown", []string{"unknown"}, false, 0, 1},
		{"known miss and unknown", []string{"miss", "unknown"}, false, 0, 1},
		{"witness before unknown", []string{"witness", "unknown"}, true, 1, 1},
		{"witness after unknown", []string{"unknown", "witness"}, true, 1, 1},
		{"known misses", []string{"miss", "miss"}, false, 0, 0},
		{"empty", nil, false, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reports []*validate.Report
			for _, outcome := range tc.runs {
				reports = append(reports, someReport(outcome))
			}
			j := Some(someExpectation()).judge(reports)
			if j.ok != tc.ok || j.n != tc.witnesses || j.unjudged != tc.unjudged {
				t.Fatalf("judgment %+v, want ok=%v witnesses=%d unjudged=%d", j, tc.ok, tc.witnesses, tc.unjudged)
			}
		})
	}
	no := Satisfies("known miss", func(*validate.Report) error { return fmt.Errorf("miss") })
	if j := Some(no, someExpectation()).judge([]*validate.Report{someReport("unknown")}); j.ok || j.unjudged != 0 {
		t.Fatalf("known false conjunct must decide the run: %+v", j)
	}
}

func TestSomeUnknownExpectationDiagnosticAndEvidence(t *testing.T) {
	for _, outcomes := range [][]string{{"unknown"}, {"miss", "unknown"}, {"unknown", "witness"}, {"miss"}} {
		t.Run(strings.Join(outcomes, "-"), func(t *testing.T) {
			var runs []*Run
			for _, outcome := range outcomes {
				runs = append(runs, &Run{Report: someReport(outcome)})
			}
			x := Runs(runs...)
			capture := &failTB{TB: t}
			done := make(chan struct{})
			go func() { //qenv:ok failTB emulates testing.T's terminating failure
				defer close(done)
				x.Expect(capture, Some(someExpectation()))
			}()
			<-done
			witness := false
			unknown := false
			for _, outcome := range outcomes {
				witness = witness || outcome == "witness"
				unknown = unknown || outcome == "unknown"
			}
			if capture.Failed() == witness {
				t.Fatalf("failure=%v, witness=%v: %v", capture.Failed(), witness, capture.errs)
			}
			text := strings.Join(capture.errs, "\n")
			if unknown && !witness {
				if !strings.Contains(text, "have no verdict") || !strings.Contains(text, "regenerate the application model") {
					t.Fatalf("claim-specific uncertainty lost its actionable cause: %s", text)
				}
			} else if strings.Contains(text, "have no verdict") || strings.Contains(text, "regenerate the application model") {
				t.Fatalf("known outcome incorrectly became inconclusive: %s", text)
			}
			// Claim-specific missing data does not invalidate the independently
			// available model conformance judgments in the evidence envelope.
			e := x.Evidence()
			if e.Unjudged != 0 || e.Conform != len(runs) {
				t.Fatalf("invented run-level unknown judgments: %+v", e)
			}
		})
	}
}
