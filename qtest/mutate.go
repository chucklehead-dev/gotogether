package qtest

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qmut"
)

// A MutationReport is what exploring the woven mutants found.
type MutationReport struct {
	// Unreached are mutants whose code no run of the baseline reached:
	// coverage's business, not the model's.
	Unreached []qmut.Mutant
	// Killed are mutants some run of whose exploration the model rejected,
	// or that broke a run (an error, a panic).
	Killed []qmut.Mutant
	// Survived are mutants reached and explored that every run survived:
	// code the model does not constrain. Each is a question: is the code
	// needed, or is the model missing what it is for?
	Survived []qmut.Mutant
	// Baseline is the exploration with every mutant off.
	Baseline *Exploration `json:"-"`
}

// Evidence is the baseline's evidence with the mutants' verdicts: the
// report a mutation run leaves.
func (r *MutationReport) Evidence() *Evidence {
	e := &Evidence{FailingBy: map[string]int{}}
	if r.Baseline != nil {
		e = r.Baseline.Evidence()
	}
	e.Mutants = r
	return e
}

// A MutationClaim is what a test expects of a mutation run
// (MutationReport.Expect).
type MutationClaim struct {
	equivalent []string
}

// NoSurvivors claims every reached mutant is killed, but those marked
// equivalent: a mutant the environment makes harmless (a dropped ack that
// is redelivered, a retry's boundary) survives by right. Mark one by its
// function, change and code, as a survivor's report prints it, which
// stays put when the code around it moves:
//
//	qtest.NoSurvivors("(Counter).Add: drop call: qobs.Record(ctx, \"replay\", nil, ...")
//
// or by its position, "file.go:42", or position and change, "file.go:42
// drop call", which a line added above it moves.
func NoSurvivors(equivalent ...string) MutationClaim { return MutationClaim{equivalent} }

// markOf is a mutant's mark by function, change and code.
func markOf(m qmut.Mutant) string {
	fn := m.Func[strings.LastIndex(m.Func, "/")+1:] // the package's path out
	if _, f, ok := strings.Cut(fn, "."); ok {
		fn = f // and its name
	}
	return fmt.Sprintf("%s: %s: %s", fn, m.Op, m.Code)
}

// marks reports whether mark e names m.
func marks(e string, m qmut.Mutant) bool {
	pos := fmt.Sprintf("%s:%d", filepath.Base(m.File), m.Line)
	return e == pos || e == pos+" "+m.Op || e == markOf(m)
}

func (c MutationClaim) marked(m qmut.Mutant) bool {
	for _, e := range c.equivalent {
		if marks(e, m) {
			return true
		}
	}
	return false
}

// Expect fails t unless the mutation run meets the claim, naming each
// surviving mutant and how to mark it, and each mark that matched no
// survivor (stale after the code changed). One claim: NoSurvivors names
// every equivalent mutant at once.
func (r *MutationReport) Expect(t testing.TB, c MutationClaim) {
	t.Helper()
	var survived []string
	used := map[string]bool{}
	for _, m := range r.Survived {
		if c.marked(m) {
			for _, e := range c.equivalent {
				if marks(e, m) {
					used[e] = true
				}
			}
			continue
		}
		survived = append(survived, fmt.Sprintf("  %s (if it is equivalent, mark it: NoSurvivors(%q))", m, markOf(m)))
	}
	var stale []string
	for _, e := range c.equivalent {
		if !used[e] {
			stale = append(stale, e)
		}
	}
	if len(survived) > 0 {
		t.Errorf("gotogether: %d mutants survived every run: code the model does not constrain, or a check missing:\n%s", len(survived), strings.Join(survived, "\n"))
	}
	if len(stale) > 0 {
		t.Errorf("gotogether: marked equivalent but no survivor there (killed now, or the code changed): %s", strings.Join(stale, ", "))
	}
}

// Score is the share of reached mutants killed.
func (r *MutationReport) Score() float64 {
	n := len(r.Killed) + len(r.Survived)
	if n == 0 {
		return 1
	}
	return float64(len(r.Killed)) / float64(n)
}

func (r *MutationReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "mutants: %d reached, %d killed (%.0f%%), %d survived, %d unreached\n",
		len(r.Killed)+len(r.Survived), len(r.Killed), 100*r.Score(), len(r.Survived), len(r.Unreached))
	for _, m := range r.Survived {
		fmt.Fprintf(&b, "  survived: %s\n", m)
	}
	return b.String()
}

// Mutate explores the system once per woven mutant (qweave -mutate; with
// none woven it skips): a baseline exploration with every mutant off,
// which must conform and says which mutants' code is reached, then each
// reached mutant with cfg's seeds. A mutant is killed
// when some run is rejected by the model, errs or panics, when no run
// records a step, or when one of claims fails for it: what the baseline
// shows besides conforming (qtest.Takes("done").InAtLeast(0.9): a step
// whose absence no single run's model check can see). Verdicts are
// shared across the explorations: most mutants' runs repeat the
// baseline's steps.
func (s System) Mutate(t testing.TB, cfg ExploreConfig, claims ...Claim) *MutationReport {
	t.Helper()
	all := qmut.Mutants()
	if len(all) == 0 {
		t.Skip("no mutants woven: run with qweave -mutate")
	}
	cfg.opts = s.opts(cfg.opts...)
	reach := &qmut.Reach{}
	base := explore(t, s.Binding, cfg, func(c qenv.Chooser) error {
		return s.Run(qenv.WithContext(c, qmut.WithReach(qenv.ContextOf(c), reach)))
	})
	base.Expect(t, append([]Claim{Conforms()}, claims...)...)
	rep := &MutationReport{Baseline: base}
	for _, m := range all {
		if !reach.Reached(m.ID) {
			rep.Unreached = append(rep.Unreached, m)
			continue
		}
		var broke atomic.Bool
		mcfg := cfg
		mcfg.emptyOK = true
		mcfg.corpusOf = base.ref() // the baseline's regressions are the mutants' too
		mut := m.String()
		mcfg.opts = append(append([]Option(nil), mcfg.opts...), func(c *settings) { c.mutant = mut }) // its evidence names it
		x := explore(t, s.Binding, mcfg, func(c qenv.Chooser) (err error) {
			defer func() {
				if r := recover(); r != nil {
					broke.Store(true) // a run the mutant crashed: killed
				}
			}()
			if s.Run(qenv.WithContext(c, qmut.WithMutant(qenv.ContextOf(c), m.ID))) != nil {
				broke.Store(true)
			}
			return nil
		})
		// killed: a run broke, or the model rejects one, or none recorded a
		// step where the baseline's did
		if broke.Load() || empty(x.Runs) || len(x.Failing()) > 0 || !x.meets(claims) {
			rep.Killed = append(rep.Killed, m)
		} else {
			rep.Survived = append(rep.Survived, m)
		}
	}
	return rep
}

// meets reports whether the exploration meets every claim, without
// failing anything: a mutant's, judged as the baseline was (verdict).
func (x *Exploration) meets(claims []Claim) bool {
	reps := x.reports()
	for _, c := range claims {
		if !x.verdict(c, reps).ok {
			return false
		}
	}
	return true
}
