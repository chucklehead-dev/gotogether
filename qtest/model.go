package qtest

import (
	"fmt"
	"github.com/chucklehead-dev/gotogether/internal/hooks"
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

// An Exercise is the claim Exercised: every model action taken, every
// option of every fault kind the runs reached taken, and every nemesis
// they enabled fired, in some run, but those it excepts.
type Exercise struct{ except map[string]bool }

// Exercised claims that the runs exercised everything: every model action
// was taken, every option of every fault kind they reached was taken
// (each by name: a drop does not stand for a duplicate), and every nemesis
// a run enabled fired (each by name: one process's crash does not stand
// for another's), in some run. except names the actions, kinds, options
// ("rpc.payments.lostResponse") and nemeses ("nemesis.crash.payments")
// not expected, a name covering those below it ("nemesis.partition"):
// each a decision the test states.
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
	const all = "every model action taken, every fault option taken and every nemesis fired"
	if len(e.except) == 0 {
		return all
	}
	var ex []string
	for x := range e.except {
		ex = append(ex, x)
	}
	sort.Strings(ex)
	return all + ", but " + strings.Join(ex, ", ")
}

func (e Exercise) judge([]*validate.Report) judgment { return judgment{show: -1} }

// excepted reports whether the except list covers name: name itself, or a
// prefix of it at a dot.
func (e Exercise) excepted(name string) bool {
	for x := range e.except {
		if name == x || strings.HasPrefix(name, x+".") {
			return true
		}
	}
	return false
}

func (e Exercise) judgeRuns(runs []*Run, actions map[string]bool) string {
	if actions == nil {
		return fmt.Sprintf("want %s, but the model's actions are unknown (no binding's model)", e)
	}
	var missing []string
	for _, a := range modelCoverage(runs, actions).Never {
		if !e.excepted(a) {
			missing = append(missing, "action "+a+" (never taken)")
		}
	}
	known, never := exercisedFaults(runs)
	for _, n := range never {
		if !e.excepted(n.name) {
			missing = append(missing, n.what)
		}
	}
	for x := range e.except {
		ok := actions[x]
		for _, r := range known {
			ok = ok || r == x || strings.HasPrefix(r, x+".")
		}
		known := ok || hooks.KnownFault(x) // declared, though no run met it (a sweep row with it off)
		if !known {
			missing = append(missing, x+" (excepted, but neither a model action, a declared fault kind or option, nor a nemesis a run enabled: misspelled?)")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Sprintf("want %s; not exercised: %s. Drive the runs to them (rates, seeds, workload), or except "+
			"them with the reason", e, strings.Join(missing, "; "))
	}
	return ""
}

// unexercised is a fault option or nemesis the runs reached and never
// took: its name as Exercised excepts it, and what the claim says of it.
type unexercised struct{ name, what string }

// exercisedFaults are the names an exception may give (every fault kind
// declared or met, each declared option, each nemesis a run enabled:
// known, so that one exception list serves runs that reach less, a sweep
// row with a kind off), and those the runs reached and never took. A
// declared kind's options count by name, those a choice offered; a kind
// of no declaration (its options unnamed) as a whole; a nemesis by name.
// A heal is not wanted: a cut heals by itself (Network.CutLength), so its
// nemesis rarely fires, and healing is the partition's end, not a fault.
func exercisedFaults(runs []*Run) (known []string, never []unexercised) {
	offers := map[string]int{} // a kind's most options at one choice: a call of a prefix (Only) offers fewer
	for _, r := range runs {
		for _, c := range r.Choices {
			if k := strings.TrimPrefix(c.Kind, "fault."); k != c.Kind {
				offers[k] = max(offers[k], c.N)
			}
		}
	}
	for _, p := range (&Exploration{Runs: runs}).faultCoverage().Points {
		if strings.HasPrefix(p.Kind, "nemesis.") {
			continue // by name, below
		}
		known = append(known, p.Kind)
		for _, name := range p.Names {
			known = append(known, p.Kind+"."+name)
		}
		if p.Reached == 0 {
			continue // a declared kind no run reached is Unreached's
		}
		if len(p.Names) == 0 {
			if p.Faulted() == 0 {
				never = append(never, unexercised{p.Kind, "fault " + p.Kind + " (reached, never faulted)"})
			}
			continue
		}
		for i, name := range p.Names {
			if i+1 >= offers[p.Kind] {
				break // no choice offered it
			}
			if i+1 >= len(p.Injected) || p.Injected[i+1] == 0 {
				never = append(never, unexercised{p.Kind + "." + name, "fault " + p.Kind + " option " + name + " (reached, never taken)"})
			}
		}
	}
	fired := map[string]bool{}
	var offered []string
	seen := map[string]bool{}
	for _, r := range runs {
		for _, c := range r.Choices {
			if strings.HasPrefix(c.Kind, "fault.nemesis") && c.Value > 0 && c.Name != "" {
				fired[c.Name] = true
			}
		}
		for _, n := range r.Nemeses {
			if !seen[n] {
				seen[n] = true
				offered = append(offered, n)
			}
		}
	}
	for _, n := range offered {
		known = append(known, "nemesis."+n)
		if !fired[n] && !strings.HasPrefix(n, "heal.") {
			never = append(never, unexercised{"nemesis." + n, "nemesis " + n + " (enabled, never fired)"})
		}
	}
	return known, never
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
