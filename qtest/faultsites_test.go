package qtest

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A site a run's code declares (DeclareFaultSite in its simulation) is
// in that exploration's coverage, unreached, and not in another's that
// declared none: it was the process's, every later test's unreached
// point.
func TestFaultCoverageNamesTheRunsOwnSites(t *testing.T) {
	scenario := func(declare bool) func(qenv.Chooser) error {
		return func(c qenv.Chooser) error {
			s := qenv.New(c)
			defer s.Close()
			s.Spawn("app", func(context.Context) {
				if declare {
					qenv.DeclareFaultSite("test.qtest.site", "pkg.Site")
				}
			})
			return s.Run(time.Second)
		}
	}
	unreached := func(declare bool) bool {
		r := runOne(settings{}, false, qenv.NewSeeded(1), scenario(declare))
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		for _, p := range (&Exploration{Runs: []*Run{r}}).faultCoverage().Unreached() {
			if p.Kind == "test.qtest.site" {
				return true
			}
		}
		return false
	}
	if !unreached(true) {
		t.Error("the declaring run's coverage does not name its site unreached")
	}
	if unreached(false) {
		t.Error("another run's coverage names the declaring run's site")
	}
}
