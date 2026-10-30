package qtest

import (
	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qenv"
	"testing"
)

func TestDrivenSimulationUsesResolvedEnvironment(t *testing.T) {
	t.Setenv("GOTOGETHER_ALL_FAULTS", "1")
	t.Setenv("GOTOGETHER_CHECK", "1")
	for _, enabled := range []bool{false, true} {
		c := settings{simEnvironment: &hooks.SimEnvironment{AllFaults: enabled, Check: enabled}}
		r := runOne(c, false, qenv.Nominal{}, func(ch qenv.Chooser) error {
			s := qenv.New(ch)
			defer s.Close()
			if s.Check != enabled || (s.Clocks.Skew != 0) != enabled {
				t.Errorf("simulation reread environment: Check=%t Clocks=%+v", s.Check, s.Clocks)
			}
			return nil
		})
		if r.Err != nil {
			t.Fatal(r.Err)
		}
	}
}
