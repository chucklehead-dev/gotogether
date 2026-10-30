package qtest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workloadmeta "github.com/chucklehead-dev/gotogether/internal/workload"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

func unreachedWorkload(c qenv.Chooser) error {
	s := qenv.New(c)
	defer s.Close()
	p := runprofile.Phase{Name: "reserve", Status: "running", Faults: "disabled", Timeout: "10ms", Started: "0s", Deadline: "10ms", Requirements: []runprofile.Requirement{{Name: "allocation", Kind: "query_result", App: "inventory", Tenant: "t1", Action: "allocation", Status: "running", Expected: []byte(`{"units":1}`), Observed: []byte(`{"units":0}`), Attempts: 1}}}
	workloadmeta.Define(s.Context(), []runprofile.Phase{p, {Name: "exercise", Status: "not_started", Faults: "configured", Timeout: "1m0s", Requirements: []runprofile.Requirement{}}})
	done := false
	s.Spawn("driver", func(ctx context.Context) {
		qobs.Record(ctx, "deposit", nil, "amount", 1, "obs.balance", 1)
		qenv.Sleep(ctx, 10*time.Millisecond)
		done = true
	})
	if _, err := s.RunUntil(time.Second, func() bool { return done }); err != nil {
		return err
	}
	p.Status = "failed"
	p.Reason = "reservation was not observed"
	p.Requirements[0].Status = "timed_out"
	workloadmeta.Update(s.Context(), 0, p)
	return missedWorkload{missing: true}
}

func TestUnreachedWorkloadRetainsTypedEvidenceWithoutModelSuccess(t *testing.T) {
	cleanProfileEnv(t)
	t.Setenv("GOTOGETHER_EVIDENCE", t.TempDir())
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB ends its goroutine; parent inspects the emitted evidence
		defer close(done)
		System{Binding: "not-loaded-for-unreached-workload.yaml", Run: unreachedWorkload}.Explore(f, ExploreConfig{Seeds: 1, Parallel: 1})
	}()
	<-done
	if !f.Failed() {
		t.Fatal("unreached setup released the exploration")
	}
	files, err := filepath.Glob(filepath.Join(evidenceDir(t.Name()), "x*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("failure evidence missing %v %v", files, err)
	}
	e, err := ReadEvidence(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if e.Conform != 0 || e.Unjudged != 1 || len(e.Executions) != 1 || len(e.Executions[0].Phases) != 2 {
		t.Fatalf("unreached workload lost or claimed success: %+v", e)
	}
	p := e.Executions[0].Phases
	if p[0].Status != "failed" || p[1].Status != "not_started" || compactJSON(p[0].Requirements[0].Observed) != `{"units":0}` {
		t.Fatalf("phase observations lost: %+v", p)
	}
	if !strings.Contains(e.String(), "reserve") || !strings.Contains(e.String(), "not reached") {
		t.Fatalf("failure hidden in text: %s", e.String())
	}
}

func TestNavigationRetainsUnreachedWorkloadSeparateFromModelHistory(t *testing.T) {
	RequireQuint(t)
	out := filepath.Join(t.TempDir(), "node.json")
	t.Setenv("GOTOGETHER_RUN", t.Name()+"#1:seed=1")
	t.Setenv("GOTOGETHER_NAV", "start")
	t.Setenv("GOTOGETHER_NAV_OUT", out)
	System{Binding: account, Run: unreachedWorkload}.Explore(t, ExploreConfig{Seeds: 1}).Expect(t, Conforms())
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var n navproto.Node
	if err := json.Unmarshal(b, &n); err != nil {
		t.Fatal(err)
	}
	if n.Receipt.Status != "pass" || n.Execution == nil || len(n.Execution.Phases) != 2 || n.Execution.Phases[0].Status != "failed" {
		t.Fatalf("workload/model scopes conflated: %+v", n)
	}
	if !strings.Contains(strings.Join(n.Notes, "\n"), "reservation was not observed") {
		t.Fatalf("execution failure absent from navigation: %+v", n.Notes)
	}
	// Only the real deposit went into model validation; phase data stayed metadata.
	if len(n.Receipt.Actors) != 1 || len(n.Model) > 1 {
		t.Fatalf("phase metadata became a synthetic model actor: %+v", n)
	}
}

func compactJSON(data []byte) string {
	var b bytes.Buffer
	if json.Compact(&b, data) != nil {
		return "invalid JSON"
	}
	return b.String()
}

func TestDeterminismIncludesWorkloadEligibility(t *testing.T) {
	a := &Run{Phases: []runprofile.Phase{{Name: "reserve", Status: "completed", Requirements: []runprofile.Requirement{{Expected: []byte(`1`), Observed: []byte(`1`)}}}}}
	b := &Run{Phases: []runprofile.Phase{a.Phases[0].Clone()}}
	if sameRun(a, b) != "" {
		t.Fatal("independent matching observations did not replay")
	}
	b.Phases[0].Requirements[0].Expected = []byte(`2`)
	if !strings.Contains(sameRun(a, b), "workload phase") {
		t.Fatal("changed setup eligibility hidden by identical native history")
	}
}
