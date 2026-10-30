package qtest

import (
	"slices"
	"strconv"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qsweep"
)

// A System's own Run serves a sweep: with no run function, the row is in
// the chooser (RowOf), and the Run deposits the row's amount. Past LIMIT
// (100) the row violates underLimit, below it the row conforms.
func TestSweepWithTheSystemsRun(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		row, ok := RowOf(c)
		if !ok {
			t.Error("no row in the chooser")
		}
		n := row.Int("amount")
		qobs.Record(qenv.ContextOf(c), "deposit", nil, "amount", n, "obs.balance", n)
		return nil
	}}
	sys.Sweep(t, SweepConfig{Factors: []qsweep.Factor{qsweep.Ints("amount", 50, 150)}, Explore: ExploreConfig{Seeds: 1},
		Expect: func(row qsweep.Row) []Claim {
			if row.Int("amount") > 100 {
				return []Claim{Some(Violates("underLimit"))}
			}
			return []Claim{Conforms()}
		}})
}

// A FaultKind factor turns its kind's faults off in the rows where it is
// off: the run's fault happens in every run of the "on" row (rate 1) and
// in none of the "off" row.
func TestSweepFaultKinds(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	kind := qenv.DeclareFaults("test.sweep", "unavailable")
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		balance := 10
		if s.FaultOf(kind) != "" {
			balance = 0 // the deposit failed but was recorded as taken: the model disagrees
		}
		qobs.Record(s.Context(), "deposit", nil, "amount", 10, "obs.balance", balance)
		return nil
	}}
	result := sys.WithRates(qenv.Rates{Default: 1}).Sweep(t, SweepConfig{Factors: []qsweep.Factor{qsweep.FaultKind("test.sweep"), qsweep.Ints("limit", 20, 30).Constant("LIMIT")},
		Explore: ExploreConfig{Seeds: 3},
		Expect: func(row qsweep.Row) []Claim {
			if row.On("test.sweep") {
				return []Claim{All(Mismatch("deposit"))}
			}
			return []Claim{Conforms()}
		}})
	for i, x := range result.Runs {
		p := x.Evidence().Profiles[0]
		off := result.Rows[i].FaultsOff()
		if !slices.Equal(p.Without, off) {
			t.Fatalf("row %s excludes %v, profile says %v", result.Rows[i], off, p.Without)
		}
		if p.Validation.Constants["LIMIT"] != strconv.Itoa(result.Rows[i].Int("limit")) || !slices.Equal(p.Validation.Invariants, []string{"nonNegative", "underLimit"}) {
			t.Fatalf("wrong model instance: %+v", p.Validation)
		}
	}
	if len(result.Evidence().Profiles) != len(result.Rows) {
		t.Fatal("aggregate lost row profiles")
	}
}
