package qenv_test

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

func TestStaticFaultSiteAppearsWithoutRecordedRuns(t *testing.T) {
	const kind, site = "test.contract.static-unreached", "contract.StaticCall"
	qenv.DeclareFaultSite(kind, site)
	cov := qenv.CoverageOf(nil)
	p := cov.Point(kind)
	if p == nil || p.Runs != 0 || len(p.Sites) != 1 || p.Sites[0] != site {
		t.Fatalf("unreached static site missing from coverage: %+v", p)
	}
	found := false
	for _, p := range cov.Unreached() {
		if p.Kind == kind {
			found = true
		}
	}
	if !found {
		t.Fatal("declared static site was not reported as unreached")
	}
}
