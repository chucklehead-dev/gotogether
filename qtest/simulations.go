package qtest

import (
	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

func simulationCalls(calls []hooks.Simulation) []runprofile.Simulation {
	out := make([]runprofile.Simulation, 0, len(calls))
	for _, call := range calls {
		s := runprofile.Simulation{Simulation: call.ID, Runtime: "deterministic", Horizon: call.Horizon.String()}
		if call.Native {
			s.Runtime = "native"
		} else {
			s.Scheduler = &runprofile.Scheduler{MaxSteps: call.MaxSteps, Hang: call.Hang.String(), StepsBefore: call.StepsBefore, StepsAfter: call.StepsAfter, StepLimitReached: call.StepLimitReached, WatchdogReached: call.WatchdogReached}
		}
		out = append(out, s)
	}
	return out
}
