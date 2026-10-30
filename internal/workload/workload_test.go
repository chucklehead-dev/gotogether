package workload_test

import (
	"context"
	"github.com/chucklehead-dev/gotogether/internal/workload"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
	"testing"
)

func TestCollectorKeepsRunsAndSnapshotsIndependent(t *testing.T) {
	a, b := &workload.Collector{}, &workload.Collector{}
	ctx := workload.With(context.Background(), a)
	phase := runprofile.Phase{Name: "reserve", Status: "not_started", Requirements: []runprofile.Requirement{{Expected: []byte(`{"units":1}`)}}}
	workload.Define(ctx, []runprofile.Phase{phase})
	phase.Requirements[0].Expected[9] = '2'
	if string(a.Phases()[0].Requirements[0].Expected) != `{"units":1}` || len(b.Phases()) != 0 {
		t.Fatal("run state leaked")
	}
	copy := a.Phases()
	copy[0].Requirements[0].Expected[9] = '3'
	if string(a.Phases()[0].Requirements[0].Expected) != `{"units":1}` {
		t.Fatal("snapshot mutated collected prerequisite")
	}
	phase.Status = "failed"
	workload.Update(ctx, 0, phase)
	if a.Phases()[0].Status != "failed" {
		t.Fatal("completion missing")
	}
}
