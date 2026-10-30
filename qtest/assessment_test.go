package qtest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func testAssessment(_ context.Context, steps []qtrace.Step) (receipt.Composition, error) {
	raw, err := json.Marshal(steps)
	if err != nil {
		return receipt.Composition{}, err
	}
	return receipt.Composition{History: fmt.Sprintf("sha256:%x", sha256.Sum256(raw)), Local: []receipt.Component{{Target: "service", Status: receipt.Pass, Applicable: true}}, Peers: []receipt.Component{{Target: "service → peer", Status: receipt.Fail, Applicable: true, Reason: "commit missing"}}, Protocol: receipt.Protocol{Component: receipt.Component{Target: "system protocol", Status: receipt.NotChecked, Reason: "not registered"}}}, nil
}

// Registration alone makes additional checks part of the ordinary assertion.
// They must never leak through the cache to the same primary history unchecked.
func TestAssessmentParticipatesInConformance(t *testing.T) {
	RequireQuint(t)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: race, Assess: testAssessment}
	r := sys.Judge(t, qenv.Nominal{})
	if r.Err != nil || r.Report.Actors[0].Conforms != validate.Pass {
		t.Fatalf("primary run: %+v", r)
	}
	if r.OK() || Conforms().Holds(r) || r.Report.Verdict().Verdict != validate.Fail || r.Report.Receipt().Status != receipt.Fail {
		t.Fatalf("registered failure passed: %s", r.Report.Summary())
	}
	if !strings.Contains(r.Report.Summary(), "peer agreement service → peer: fail") {
		t.Fatal("failure not actionable")
	}
	d := validate.NewDocument("validate")
	d.AddReport(r.Report)
	if d.Receipt.Composition == nil {
		t.Fatal("composition missing in JSON document")
	}
	sys.Assess = nil
	unregistered := sys.Judge(t, qenv.Nominal{})
	if !Conforms().Holds(unregistered) || unregistered.Report.Composition != nil {
		t.Fatal("assessment leaked into a different run's cached verdict")
	}
}

func TestAssessmentRejectsWrongHistoryAndMutation(t *testing.T) {
	for _, mode := range []string{"wrong history", "mutation"} {
		t.Run(mode, func(t *testing.T) {
			r := &Run{Recorded: []qtrace.Step{{Recorder: "r", Seq: 1, Action: "op"}}, Report: &validate.Report{Actors: []*validate.Result{{Actor: "a", Conforms: validate.Pass}}}}
			c := settings{assess: func(ctx context.Context, steps []qtrace.Step) (receipt.Composition, error) {
				s, err := testAssessment(ctx, steps)
				s.Peers = nil
				if mode == "wrong history" {
					s.History = "sha256:other"
				} else {
					steps[0].Action = "changed"
				}
				return s, err
			}}
			assessHistory(t.Context(), c, r)
			r.adjust(nil)
			if r.Err != nil || r.Report.Composition == nil || r.Report.Composition.Error == "" || r.Report.Receipt().Status != receipt.Error || r.OK() {
				t.Fatalf("invalid assessment accepted: %+v", r.Report.Receipt())
			}
		})
	}
}

func TestAssessmentNavigationRecomputesWholeHistory(t *testing.T) {
	RequireQuint(t)
	out := filepath.Join(t.TempDir(), "node.json")
	t.Setenv("GOTOGETHER_RUN", t.Name()+"#1:seed=1")
	t.Setenv("GOTOGETHER_NAV", "start")
	t.Setenv("GOTOGETHER_NAV_OUT", out)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: race, Assess: testAssessment}
	sys.Explore(t, ExploreConfig{Seeds: 1, Chooser: func(uint64) qenv.Chooser { return qenv.Nominal{} }}).Expect(t, Conforms())
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var n navproto.Node
	if err = json.Unmarshal(raw, &n); err != nil {
		t.Fatal(err)
	}
	if n.Receipt.Status != receipt.Fail || n.Receipt.Composition == nil || n.Receipt.Composition.Run != n.Run || n.Receipt.Composition.History == "" || len(n.Steps) != 0 {
		t.Fatalf("cursor mistaken for whole-history check: %+v", n)
	}
}
