package qtest

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

func TestFailureEvidenceHasNoInventedJudgmentsAndRetainsOrigins(t *testing.T) {
	x := &Exploration{test: "TestFailure", id: 1, profile: &runprofile.Profile{ID: "TestFailure#1"}, Runs: []*Run{{origin: origin{kind: "seed", seed: 2}, Err: errors.New("no database")}, nil}}
	e := x.failedEvidence([]origin{{kind: "seed", seed: 2}, {kind: "corpus", value: "saved.json"}})
	if e.Runs != 2 || e.Unjudged != 2 || e.Conform != 0 || len(e.FailingBy) != 0 || e.Sample != nil || e.Model != nil || e.Judges != nil {
		t.Fatalf("unjudged execution became model evidence: %+v", e)
	}
	if e.RunErrors["TestFailure#1:seed=2"] != "no database" || e.RunErrors["TestFailure#1:corpus=saved.json"] == "" || len(e.Profiles) != 1 {
		t.Fatalf("failure identity: %+v", e)
	}
}

func TestStepLimitFailureEmitsMachineReadableEvidence(t *testing.T) {
	cleanProfileEnv(t)
	t.Setenv("GOTOGETHER_RUN", "OtherTest#1:seed=1")
	t.Setenv("GOTOGETHER_EVIDENCE", t.TempDir())
	t.Setenv("GOTOGETHER_SESSION", "limits-test")
	f := &failTB{TB: t}
	sys := System{Binding: "not-loaded-before-execution-error.yaml", Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		s.MaxSteps, s.Hang = 2, 0
		s.Spawn("busy", func(ctx context.Context) {
			for {
				qenv.Yield(ctx)
			}
		})
		return s.Run(time.Minute)
	}}
	done := make(chan struct{})
	go func() { //qenv:ok failTB ends its goroutine like testing.T; the parent inspects persisted evidence
		defer close(done)
		sys.Explore(f, ExploreConfig{Seeds: 1, Parallel: 1})
	}()
	<-done
	if !f.Failed() {
		t.Fatal("limit failure did not stop exploration")
	}
	files, err := filepath.Glob(filepath.Join(evidenceDir(t.Name()), "x*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("failure evidence files %v: %v; errors %v logs %v", files, err, f.errs, f.logs)
	}
	e, err := ReadEvidence(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if e.Unjudged != 1 || e.Conform != 0 || len(e.FailingBy) != 0 || len(e.Executions) != 1 || len(e.Profiles) != 1 || e.Session != "limits-test" {
		t.Fatalf("failure JSON: %+v", e)
	}
	execution := e.Executions[0]
	if !strings.HasSuffix(execution.Run, ":seed=1") || len(execution.Simulations) != 1 || execution.Simulations[0].Scheduler == nil || !execution.Simulations[0].Scheduler.StepLimitReached || !strings.Contains(e.RunErrors[execution.Run], "too many scheduling steps") {
		t.Fatalf("actual cap exhaustion missing: %+v", e)
	}
}
