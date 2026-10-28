package counters

import (
	"bytes"
	"encoding/json"
	"os"
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
		Expect(t, qtest.Conforms(), qtest.All(qtest.Completes()), qtest.All(gristtest.ReadersFirst()), qtest.All(gristtest.NoClientErrors()))
}

// release1Catalog is release 1's catalog as deployed: what CI keeps of
// the release that runs (`counters catalog > deployed.json` by its
// binary), for `go run ./cmd/counters catalog -diff release1/catalog.json`
// before release 2 ships (docs/tutorials/grist-on-kubernetes.md).
const release1Catalog = "release1/catalog.json"

// The kept catalog is release 1's (GRIST_UPDATE=1 writes it again): a
// catalog -diff against a stale one would judge the rollout of another
// release.
func TestRelease1Catalog(t *testing.T) {
	b, err := json.MarshalIndent(release1.App.Catalog(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if os.Getenv("GRIST_UPDATE") != "" {
		if err := os.WriteFile(release1Catalog, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	kept, err := os.ReadFile(release1Catalog)
	if err != nil || !bytes.Equal(kept, b) {
		t.Fatalf("%s is not release 1's catalog (%v): GRIST_UPDATE=1 go test -run TestRelease1Catalog . writes it", release1Catalog, err)
	}
}
