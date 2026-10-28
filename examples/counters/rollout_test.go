package counters

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/counters/release1"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// upgrade is the rolling update from release 1 (the package release1, as
// deployed) to this release, judged by binding: the rolling restart's
// shape (rolling: two nodes behind a lagging router, drained as the
// binary drains, a disruption budget of one), each node's replacement
// running this release, so a run passes through every mix of the two
// while clients create, increment, show and retire counters.
func upgrade(t *testing.T, binding string, opts ...qtest.Option) qtest.System {
	cfg := rolling
	cfg.Setup = setup
	s := gristtest.Upgrade(t, binding, release1.App, App, cfg).WithRates(lagging)
	s.Opts = opts
	return s
}

// The rolling update from release 1, whose counter-created is version 1
// (creator), to this release, which reads version 2 (owner) and writes
// version 1 (WritesVersion): every run conforms to this release's model
// and settles, release 1's steps among them (recorded in version 2's
// shape); at the clients every call is answered, linearizes, none was
// refused by a node of release 1 (ReadersFirst: every event it meets is
// one it reads) or by a node at all (NoClientErrors). Writing version 2
// at once (the eagerV2 mutant) leaves a node of release 1 meeting an
// event it cannot read: it refuses the call, a node of release 2 answers
// it (TestMutant).
func TestRollingUpdate(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	n := qtest.ExploreConfig{Seeds: 10, ShortSeeds: 2}
	upgrade(t, retiring).Explore(t, n).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Takes("retireCounter").Outcome("ok"))
	upgrade(t, calls, qtest.Linearize()).Explore(t, n).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Completes()), gristtest.ReadersFirst(), gristtest.NoClientErrors())
}
