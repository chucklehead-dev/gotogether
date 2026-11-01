package qtest

import (
	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

// execution is a fresh observation snapshot, never a configuration or judgment.
func (r *Run) execution(name string) runprofile.Execution {
	return (runprofile.Execution{Run: name, Simulations: r.simulations, Phases: r.Phases, Settlement: r.settlement}).Clone()
}

func (r *Run) hasExecution() bool {
	return r != nil && (len(r.simulations) > 0 || len(r.Phases) > 0 || r.settlement != nil)
}

func unsettledObservation(u *qenv.Unsettled) *runprofile.Settlement {
	s := &runprofile.Settlement{After: u.After.String(), Tasks: append([]string{}, u.Tasks...), DownProcesses: []runprofile.ProcessIdentity{}}
	for _, p := range u.DownProcesses {
		s.DownProcesses = append(s.DownProcesses, runprofile.ProcessIdentity{Name: p.Name, Incarnation: p.Incarnation, Index: p.Index})
	}
	return s
}

func simulationCalls(calls []hooks.Simulation) []runprofile.Simulation {
	out := make([]runprofile.Simulation, 0, len(calls))
	for _, call := range calls {
		s := runprofile.Simulation{Operation: call.Operation, Simulation: call.ID, Runtime: "deterministic", Horizon: call.Horizon.String()}
		if call.Native {
			s.Runtime = "native"
		} else {
			s.Scheduler = &runprofile.Scheduler{MaxSteps: call.MaxSteps, Hang: call.Hang.String(), StepsBefore: call.StepsBefore, StepsAfter: call.StepsAfter, StepLimitReached: call.StepLimitReached, WatchdogReached: call.WatchdogReached}
		}
		out = append(out, s)
	}
	return out
}
