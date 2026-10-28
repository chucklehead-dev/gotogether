package qmut_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qmut"
	"github.com/chucklehead-dev/gotogether/woven"
)

// Woven code calls woven.Cond and woven.Pick from a run's tasks: the run's mutant
// changes them, the others' runs and code outside a run see the original.
func TestSwitchesFollowTheRun(t *testing.T) {
	base := woven.RegisterMutants([]woven.Mutant{{Op: "negate if"}, {Op: "<= for <"}})
	run := func(ctx context.Context) (cond, pick bool) {
		sim := qenv.New(qenv.WithContext(qenv.NewSeeded(1), ctx))
		sim.Process("p", false, func(context.Context) {
			cond, pick = woven.Cond(base, true), woven.Pick(base+1, false, true)
		})
		if err := sim.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		sim.Close()
		return
	}
	r := &qmut.Reach{}
	if c, p := run(qmut.WithReach(context.Background(), r)); !c || p {
		t.Errorf("no mutant: Cond %v, Pick %v; want the originals", c, p)
	}
	if !r.Reached(base) || !r.Reached(base+1) {
		t.Error("reach did not note both mutants")
	}
	if c, _ := run(qmut.WithMutant(context.Background(), base)); c {
		t.Error("mutant on: Cond not negated")
	}
	if _, p := run(qmut.WithMutant(context.Background(), base+1)); !p {
		t.Error("mutant on: Pick not mutated")
	}
	if !woven.Cond(base, true) {
		t.Error("outside a run: Cond changed")
	}
}
