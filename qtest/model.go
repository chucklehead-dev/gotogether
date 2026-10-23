package qtest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate"
)

// ModelCoverage says which of the model's actions the runs took: an
// exploration that never took an action says nothing about it, however
// many runs conform.
type ModelCoverage struct {
	// Taken counts, for each model action, the runs that took it.
	Taken map[string]int `json:"taken"`
	// Never are the model's actions no run took (init and step aside).
	Never []string `json:"never,omitempty"`
}

// taken returns the model actions a run's replay took: every actor's
// calls up to its failure (a step the model could not take was not taken;
// a step whose state did not match was).
func taken(r *Run) map[string]bool {
	out := map[string]bool{}
	if r == nil || r.Report == nil {
		return out
	}
	for _, a := range r.Report.Actors {
		calls := a.Calls
		if f := a.Failure; f != nil && a.Conforms != validate.Pass && f.Index >= 0 && f.Index <= len(calls) {
			end := f.Index
			if f.Kind == "state" && end < len(calls) {
				end++
			}
			calls = calls[:end]
		}
		for _, c := range calls {
			if c.Action != "" {
				out[c.Action] = true
			}
		}
	}
	return out
}

func modelCoverage(runs []*Run, actions map[string]bool) *ModelCoverage {
	m := &ModelCoverage{Taken: map[string]int{}}
	for _, r := range runs {
		for a := range taken(r) {
			m.Taken[a]++
		}
	}
	for a := range actions {
		if a != "init" && a != "step" && m.Taken[a] == 0 {
			m.Never = append(m.Never, a)
		}
	}
	sort.Strings(m.Never)
	return m
}

// A Take is a claim that runs took a model action, in at least a share of
// them (Takes): the twin of Reaches for the model's side. A pass in which
// no run cancelled says nothing of cancelling.
type Take struct {
	action string
	share  float64
}

// Takes claims that some run took the model action.
func Takes(action string) Take { return Take{action: action} }

// InAtLeast wants the action taken in at least share (0 to 1) of the runs.
func (k Take) InAtLeast(share float64) Take { k.share = share; return k }

func (k Take) String() string {
	if k.share > 0 {
		return fmt.Sprintf("taking %s in at least %.0f%% of the runs", k.action, 100*k.share)
	}
	return "some run taking " + k.action
}

func (k Take) judge([]*validate.Report) judgment { return judgment{show: -1} }

func (k Take) judgeRuns(runs []*Run, actions map[string]bool) string {
	if actions != nil && !actions[k.action] {
		return fmt.Sprintf("%s names %s, which the model has no action of: a misspelled name never matches", k, k.action)
	}
	m := modelCoverage(runs, actions)
	need := 1
	if k.share > 0 {
		need = int(k.share*float64(len(runs)) + 0.999999)
	}
	if n := m.Taken[k.action]; n < need {
		return fmt.Sprintf("want %s; %d of %d runs did (the actions the runs took: %s). Drive the system to it (more seeds, "+
			"a longer run, a workload that asks for it) or say why it is not expected", k, n, len(runs), takenList(m))
	}
	return ""
}

// An Exercise is the claim Exercised: every model action taken, and every
// fault kind the runs reached faulted, in some run, but those it excepts.
type Exercise struct{ except map[string]bool }

// Exercised claims that the runs exercised everything: every model action
// was taken and every fault kind they reached was faulted, in some run.
// except names the actions and kinds not expected (a recovery path the
// workload never needs), each a decision the test states.
func Exercised(except ...string) Exercise {
	e := Exercise{except: map[string]bool{}}
	for _, x := range except {
		if strings.HasPrefix(x, "fault.") {
			panic(fmt.Sprintf("qtest: Exercised(%q): name the kind without the \"fault.\" prefix (%q), as reports print it", x, strings.TrimPrefix(x, "fault.")))
		}
		e.except[x] = true
	}
	return e
}

func (e Exercise) String() string {
	if len(e.except) == 0 {
		return "every model action taken and every fault kind faulted"
	}
	var ex []string
	for x := range e.except {
		ex = append(ex, x)
	}
	sort.Strings(ex)
	return "every model action taken and every fault kind faulted, but " + strings.Join(ex, ", ")
}

func (e Exercise) judge([]*validate.Report) judgment { return judgment{show: -1} }

func (e Exercise) judgeRuns(runs []*Run, actions map[string]bool) string {
	if actions == nil {
		return fmt.Sprintf("want %s, but the model's actions are unknown (no binding's model)", e)
	}
	var missing []string
	for _, a := range modelCoverage(runs, actions).Never {
		if !e.except[a] {
			missing = append(missing, "action "+a+" (never taken)")
		}
	}
	for _, p := range (&Exploration{Runs: runs}).faultCoverage().Points {
		if p.Faulted() == 0 && !e.except[p.Kind] {
			missing = append(missing, "fault "+p.Kind+" (reached, never faulted)")
		}
	}
	for x := range e.except {
		if !actions[x] && !reachedKind(runs, x) {
			missing = append(missing, x+" (excepted, but neither a model action nor a kind the runs reached: misspelled?)")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Sprintf("want %s; not exercised: %s. Drive the runs to them (rates, seeds, workload), or except "+
			"them with the reason", e, strings.Join(missing, "; "))
	}
	return ""
}

func reachedKind(runs []*Run, kind string) bool {
	for _, p := range (&Exploration{Runs: runs}).faultCoverage().Points {
		if p.Kind == kind {
			return true
		}
	}
	return false
}

func takenList(m *ModelCoverage) string {
	var as []string
	for a := range m.Taken {
		as = append(as, a)
	}
	sort.Strings(as)
	if len(as) == 0 {
		return "none"
	}
	return strings.Join(as, ", ")
}
