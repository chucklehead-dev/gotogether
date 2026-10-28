package gristtest

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// Upgrade is a rolling update from from, the deployed release, to app,
// its next, as a qtest.System judged by binding (gristsim.System with
// cfg.From = from): nodes of both releases on one store, from's upgraded
// one at a time while the clients call (a Cluster's rolling restart, if
// cfg has one with Rolling, is the rollout), every step judged by app's
// model. Explore it, and its clients' side (WithBinding, with
// qtest.Linearize), and claim what a rollout must keep:
//
//	up := gristtest.Upgrade(t, "shop.binding.yaml", deployed.App, App, cfg)
//	up.Explore(t, n).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
//	up.WithBinding("shop.calls.binding.yaml", qtest.Linearize()).Explore(t, n).
//		Expect(t, qtest.Conforms(), qtest.All(qtest.Completes()), qtest.All(gristtest.ReadersFirst()))
//
// A node of from never decides from an event it cannot read: its reads
// of one refuse the call (retried, counted: ReadersFirst) and its
// processors wait for a node of app. from is the deployed release's App,
// a package of its own (the release as deployed, kept for this test).
// Upgrade first compares their catalogs, as `<app> catalog -diff` does,
// logging the changes and failing t on a breaking one, which no rollout
// survives.
func Upgrade(t testing.TB, binding string, from, app *grist.App, cfg gristsim.Config) qtest.System {
	t.Helper()
	old, err := json.Marshal(from.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	cur, err := json.Marshal(app.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	d, err := catalog.CompareCatalogs(old, cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Changes) > 0 {
		t.Logf("gristtest.Upgrade: %s's catalog against the deployed release's: %s", app.Name, strings.TrimSpace(d.String()))
	}
	if d.Verdict() == catalog.Breaking {
		t.Fatalf("gristtest.Upgrade: %s's catalog breaks the deployed release's: no rolling update survives it:\n%s", app.Name, d)
	}
	cfg.From = from
	return gristsim.System(binding, app, cfg)
}
