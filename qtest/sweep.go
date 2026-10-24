package qtest

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qrun"
	"github.com/chucklehead-dev/gotogether/qsweep"
)

// A SweepConfig explores a parameter space by covering rows (qsweep.Cover):
// every combination of Strength factors' values (default 2, pairwise) is
// in some row, each row explored with its own seeds. Make fault kinds
// factors (qsweep.Bool, then qenv.Without for the kinds a row turns off)
// and the sweep is swarm testing with a guarantee: every pair of kinds
// runs together, and alone, with no other kind masking it.
type SweepConfig struct {
	Factors  []qsweep.Factor
	Strength int
	// Allow rules rows out (nil: all allowed); combinations only ruled-out
	// rows have are not required.
	Allow func(qsweep.Row) bool
	// Explore is each row's exploration (Seeds per row).
	Explore ExploreConfig
	// Opts adds a row's options to Explore's. A factor that names its
	// model constant (qsweep.Factor.Constant) needs none: each row is
	// checked with the constants of its factors (Row.Constants).
	Opts func(qsweep.Row) []Option
	// Expect is what each row's runs should show (nil: Conforms): a
	// mutant's bug is real under some rows only.
	Expect func(qsweep.Row) []Claim
}

// SweepResult is a sweep's rows and their explorations.
type SweepResult struct {
	Rows []qsweep.Row
	Runs []*Exploration
}

// faultCoverage is the fault coverage over every row's runs.
func (s *SweepResult) faultCoverage() *qenv.Coverage {
	var runs [][]qenv.Choice
	for _, x := range s.Runs {
		if x == nil { // a row that never ran (filtered by -run, or a Fatal before)
			continue
		}
		for _, r := range x.Runs {
			runs = append(runs, r.Choices)
		}
	}
	return qenv.CoverageOf(runs)
}

var rowKey = qrun.NewKey[qsweep.Row]("sweep.row")

// RowOf is the sweep row a run's chooser was made for (System.Sweep): a
// System's Run configures itself from it (sizes, mutations); the row's
// fault kinds that are off are off in the chooser already.
func RowOf(c qenv.Chooser) (qsweep.Row, bool) { return qrun.InContext(qenv.ContextOf(c), rowKey) }

func sweep(t *testing.T, bindingPath string, sw SweepConfig, base exploreSpec, scenario func(c qenv.Chooser) error) *SweepResult {
	t.Helper()
	strength := sw.Strength
	if strength == 0 {
		strength = 2
	}
	rows, err := qsweep.Cover(sw.Factors, strength, sw.Allow)
	if err != nil {
		t.Fatal(err)
	}
	s := &SweepResult{Rows: rows, Runs: make([]*Exploration, len(rows))}
	t.Logf("qtest: %d rows cover every %d-way combination of %d factors", len(rows), strength, len(sw.Factors))
	for i, row := range rows {
		t.Run(row.String(), func(t *testing.T) {
			xc := base
			if cs := row.Constants(); len(cs) > 0 {
				xc.opts = append(append([]Option(nil), xc.opts...), Constants(cs))
			}
			if sw.Opts != nil {
				xc.opts = append(append([]Option(nil), xc.opts...), sw.Opts(row)...)
			}
			off := row.FaultsOff()
			x := explore(t, bindingPath, xc, func(c qenv.Chooser) error {
				c = qenv.WithContext(c, qrun.With(qenv.ContextOf(c), rowKey, row))
				if len(off) > 0 {
					c = qenv.Without(c, off...)
				}
				return scenario(c)
			})
			s.Runs[i] = x
			want := []Claim{Conforms()}
			if sw.Expect != nil {
				want = sw.Expect(row)
			}
			x.Expect(t, want...)
		})
	}
	var kinds []string // once per sweep, after its rows declared what they make
	for _, f := range sw.Factors {
		if f.Fault {
			kinds = append(kinds, f.Name)
		}
	}
	var runs []*Run
	for _, x := range s.Runs {
		if x != nil {
			runs = append(runs, x.Runs...)
		}
	}
	if ran(runs) { // a row's run that did not run declared nothing: its row's test says why
		unknownKinds(t, "qsweep.FaultKind", kinds, runs)
	}
	return s
}
