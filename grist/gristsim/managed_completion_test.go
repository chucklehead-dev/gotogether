package gristsim_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// A fault trigger not yet eligible and an operator without a rewind hold no
// application work. Their waits cannot prevent an otherwise finished run.
func TestManagedIdleMonitorsSettle(t *testing.T) {
	sink := &qobs.MemorySink{}
	chooser := qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink)))
	cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 1, Send: []string{"note"},
		Store: &gristsim.Managed{LoseAfter: 1000, Resume: time.Minute}}
	if err := gristsim.Run(chooser, app(), cfg); err != nil {
		t.Fatal(err)
	}
	settled := false
	for _, s := range sink.Steps() {
		if s.Action == "gristRewound" {
			t.Fatal("ineligible incident ran")
		}
		settled = settled || s.Settled
	}
	if !settled {
		t.Fatal("idle monitors prevented settlement")
	}
}
