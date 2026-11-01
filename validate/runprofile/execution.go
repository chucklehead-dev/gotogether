package runprofile

import (
	"fmt"
	"strings"
)

// Execution records simulator limits, workload observations and terminal
// incomplete settlement as actually observed in one driven run. These facts
// are separate from the exploration profile:
// scenarios may change limits between calls or create several simulations.
type Execution struct {
	Run         string       `json:"run"`
	Simulations []Simulation `json:"simulations"`
	Phases      []Phase      `json:"phases,omitempty"`
	// Settlement is present when the scenario returned an unsettled outcome.
	// It is runtime evidence, separate from the model's history judgment.
	Settlement *Settlement `json:"settlement,omitempty"`
}

// ProcessIdentity identifies an observed process incarnation within one Sim.
// Index is its 1-based creation position; names need not be unique. These
// identities are local to the simulation, never global process identifiers.
type ProcessIdentity struct {
	Name        string `json:"name"`
	Incarnation int    `json:"incarnation"`
	Index       int    `json:"index"`
}

// Settlement records an incomplete terminal settlement attempt. DownProcesses
// lists down processes required by that attempt's scope; it makes no assertion
// about business work held by killed tasks or processes outside a SettleCore.
// After is the requested bound, not observed elapsed time. Process indices are
// local to the Sim that returned this outcome, not indices into Simulations.
type Settlement struct {
	After         string            `json:"after"`
	Tasks         []string          `json:"tasks"`
	DownProcesses []ProcessIdentity `json:"downProcesses"`
}

// Clone returns an independent runtime observation snapshot.
func (s *Settlement) Clone() *Settlement {
	if s == nil {
		return nil
	}
	copy := *s
	copy.Tasks = append([]string{}, s.Tasks...)
	copy.DownProcesses = append([]ProcessIdentity{}, s.DownProcesses...)
	return &copy
}

// String describes observed incomplete settlement without judging the model.
func (s *Settlement) String() string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "runtime did not settle (bound %s)", s.After)
	if len(s.DownProcesses) > 0 {
		b.WriteString("; required processes down:")
		for _, p := range s.DownProcesses[:min(len(s.DownProcesses), 8)] {
			fmt.Fprintf(&b, "\n  %q#%d (process %d)", p.Name, p.Incarnation, p.Index)
		}
		if len(s.DownProcesses) > 8 {
			fmt.Fprintf(&b, "\n  %d more down processes; JSON retains every identity", len(s.DownProcesses)-8)
		}
	}
	if len(s.Tasks) > 0 {
		b.WriteString("; tasks still at work or waiting:")
		for _, task := range s.Tasks[:min(len(s.Tasks), 8)] {
			fmt.Fprintf(&b, "\n  %s", task)
		}
		if len(s.Tasks) > 8 {
			fmt.Fprintf(&b, "\n  %d more tasks; JSON retains every task", len(s.Tasks)-8)
		}
	}
	return b.String()
}

// Clone returns an independent snapshot, including each scheduler observation.
func (e Execution) Clone() Execution {
	copy := e
	copy.Settlement = e.Settlement.Clone()
	copy.Phases = make([]Phase, len(e.Phases))
	for i, p := range e.Phases {
		copy.Phases[i] = p.Clone()
	}
	copy.Simulations = make([]Simulation, len(e.Simulations))
	for i, call := range e.Simulations {
		copy.Simulations[i] = call
		if call.Scheduler != nil {
			scheduler := *call.Scheduler
			copy.Simulations[i].Scheduler = &scheduler
		}
	}
	return copy
}

// ExecutionsString summarizes observed policies across runs without claiming
// their cumulative scheduler counters form one sequence. Every reached limit
// contributes to the totals; incomplete settlements retain their run identity.
func ExecutionsString(executions []Execution) string {
	if len(executions) == 0 {
		return ""
	}
	combined := Execution{Run: fmt.Sprintf("%d %s", len(executions), countNoun(len(executions), "run"))}
	for _, e := range executions {
		combined.Simulations = append(combined.Simulations, e.Simulations...)
	}
	var b strings.Builder
	b.WriteString(combined.String())
	shown := 0
	for _, e := range executions {
		if e.Settlement == nil {
			continue
		}
		if shown < 8 {
			fmt.Fprintf(&b, "\nsettlement %s: %s", e.Run, e.Settlement.String())
		}
		shown++
	}
	if shown > 8 {
		fmt.Fprintf(&b, "\n%d more incomplete settlements; JSON retains each run", shown-8)
	}
	return b.String()
}

// Simulation is one Sim.Run invocation. Simulation identifies its instance
// within the driven run, starting at 1. Horizon is the duration requested of
// this call, not elapsed time or an assertion of quiescence.
type Simulation struct {
	Operation  string     `json:"operation"`
	Simulation int        `json:"simulation"`
	Runtime    string     `json:"runtime"`
	Horizon    string     `json:"horizon"`
	Scheduler  *Scheduler `json:"scheduler,omitempty"`
}

// Scheduler records effective deterministic scheduler limits and cumulative
// scheduling steps. Native runtimes do not enforce these limits and omit it.
// Hang is a wall-clock watchdog duration; "0s" disables that watchdog.
type Scheduler struct {
	MaxSteps         int    `json:"maxSteps"`
	Hang             string `json:"hang"`
	StepsBefore      int    `json:"stepsBefore"`
	StepsAfter       int    `json:"stepsAfter"`
	StepLimitReached bool   `json:"stepLimitReached"`
	WatchdogReached  bool   `json:"watchdogReached"`
}

// String renders observed limits and incomplete settlement independently of
// configuration and model judgment.
func (e Execution) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "simulation limits %s: %d %s", e.Run, len(e.Simulations), countNoun(len(e.Simulations), "call"))
	if len(e.Simulations) == 0 {
		b.WriteString("; no qenv Sim.Run calls observed")
	}
	type policy struct {
		runtime, horizon, hang string
		maxSteps               int
	}
	type group struct {
		policy
		count, first, last, limited, hung int
	}
	var groups []group
	indices := map[policy]int{}
	limited, hung := 0, 0
	for _, call := range e.Simulations {
		p := policy{runtime: call.Runtime, horizon: call.Horizon}
		if s := call.Scheduler; s != nil {
			p.hang, p.maxSteps = s.Hang, s.MaxSteps
		}
		index, ok := indices[p]
		if !ok {
			index = len(groups)
			indices[p] = index
			g := group{policy: p}
			if s := call.Scheduler; s != nil {
				g.first = s.StepsBefore
			}
			groups = append(groups, g)
		}
		g := &groups[index]
		g.count++
		if s := call.Scheduler; s != nil {
			g.first, g.last = min(g.first, s.StepsBefore), max(g.last, s.StepsAfter)
			if s.StepLimitReached {
				g.limited++
				limited++
			}
			if s.WatchdogReached {
				g.hung++
				hung++
			}
		}
	}
	if limited > 0 {
		fmt.Fprintf(&b, " step limit reached in %d %s", limited, countNoun(limited, "call"))
	}
	if hung > 0 {
		fmt.Fprintf(&b, " watchdog reached in %d %s", hung, countNoun(hung, "call"))
	}
	for _, g := range groups[:min(len(groups), 8)] {
		fmt.Fprintf(&b, "\n  %d %s %s: horizon %s", g.count, g.runtime, countNoun(g.count, "call"), g.horizon)
		if g.runtime == "deterministic" {
			fmt.Fprintf(&b, "; observed step range %d–%d; cap %d; watchdog %s", g.first, g.last, g.maxSteps, g.hang)
			if g.limited > 0 {
				fmt.Fprintf(&b, "; step limit reached in %d %s", g.limited, countNoun(g.limited, "call"))
			}
			if g.hung > 0 {
				fmt.Fprintf(&b, "; watchdog reached in %d %s", g.hung, countNoun(g.hung, "call"))
			}
		}
	}
	if len(groups) > 8 {
		fmt.Fprintf(&b, "\n  %d more simulation policies; JSON retains every call", len(groups)-8)
	}
	if e.Settlement != nil {
		fmt.Fprintf(&b, "\nsettlement %s: %s", e.Run, e.Settlement.String())
	}
	return b.String()
}

func countNoun(n int, singular string) string {
	if n == 1 {
		return singular
	}
	return singular + "s"
}
