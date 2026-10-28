package gristsim_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// clusterRun runs app() under cl at seed with rates, returning how many
// of the clients' calls had a try refused, and the run's log.
func clusterRun(t *testing.T, seed uint64, rates qenv.Rates, cl gristsim.Cluster) (refused int, log []string) {
	t.Helper()
	sink := &qobs.MemorySink{}
	base := qenv.NewSeeded(seed)
	base.Rates = rates
	c := qenv.WithContext(base, qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink)))
	if err := gristsim.Run(c, app(), gristsim.Config{Check: true, Cluster: &cl, Ops: 8, Log: func(l string) { log = append(log, l) }}); err != nil {
		t.Fatal(err)
	}
	for _, s := range sink.Steps() {
		if s.Args["refused"] != nil {
			refused++
		}
	}
	return refused, log
}

// count is how many lines of log contain s.
func count(log []string, s string) int {
	n := 0
	for _, l := range log {
		if strings.Contains(l, s) {
			n++
		}
	}
	return n
}

// A rolling restart of three nodes behind a router that learns of each
// change late (route.api.lag stale at every change): with the drain's
// delay (grist.DrainDelay, 5 s) longer than the router's lag (3 s), every
// node drains and exits within its grace, is restarted, and no client's
// try is refused; with no delay (the server shut at the signal) the
// router sends tries to a node that no longer takes them, in some run;
// with no lag, nothing is refused even without the delay.
func TestClusterRollingRestart(t *testing.T) {
	stale := qenv.NoFaults.Set("route.api.lag", 1)
	lagged := gristsim.Cluster{Rolling: true, EndpointLag: 3 * time.Second}
	noDelay := lagged
	noDelay.Drain.Delay = -1
	noLag := noDelay
	noLag.EndpointLag = 0
	refused := map[string]int{}
	for seed := uint64(1); seed <= 6; seed++ {
		n, log := clusterRun(t, seed, stale, lagged)
		if n != 0 || count(log, "exit node") != 3 || count(log, "kill") != 0 || count(log, "terminate node") != 3 {
			t.Errorf("seed %d, the delay longer than the lag: %d calls refused, %d exits, %d kills: want none refused, each node exited once, none killed\n%s",
				seed, n, count(log, "exit node"), count(log, "kill"), strings.Join(log, "\n"))
		}
		n, _ = clusterRun(t, seed, stale, noDelay)
		refused["no delay"] += n
		n, _ = clusterRun(t, seed, stale, noLag)
		refused["no lag"] += n
	}
	if refused["no delay"] == 0 || refused["no lag"] != 0 {
		t.Errorf("calls refused over 6 seeds: %v: want some without the delay, none without the lag", refused)
	}
}

// A disruption budget of one keeps the terminate nemeses from draining
// two nodes at once, however often they fire; without it they do in
// some run, and the router's view empties.
func TestClusterDisruptions(t *testing.T) {
	often := qenv.NoFaults.Set("nemesis.terminate", 0.2)
	overlap := func(log []string) bool {
		draining := map[string]bool{}
		for _, l := range log {
			f := strings.Fields(l)
			if len(f) < 3 {
				continue
			}
			node, _, _ := strings.Cut(f[2], "#")
			switch f[1] {
			case "terminate":
				draining[node] = true
				if len(draining) > 1 {
					return true
				}
			case "exit", "kill", "crash":
				delete(draining, node)
			}
		}
		return false
	}
	some := false
	for seed := uint64(1); seed <= 6; seed++ {
		if _, log := clusterRun(t, seed, often, gristsim.Cluster{Disruptions: 1}); overlap(log) {
			t.Errorf("seed %d: two nodes draining at once under a budget of one\n%s", seed, strings.Join(log, "\n"))
		}
		_, log := clusterRun(t, seed, often, gristsim.Cluster{})
		some = some || overlap(log)
	}
	if !some {
		t.Error("without a budget no run drained two nodes at once: the claim's case never happened")
	}
}

// A cluster's nodes are qenv.Services behind its router: not on the HTTP
// tier yet, which says so.
func TestClusterNotHTTP(t *testing.T) {
	err := gristsim.Run(qenv.NewSeeded(1), app(), gristsim.Config{HTTP: true, Cluster: &gristsim.Cluster{}})
	if err == nil || !strings.Contains(err.Error(), "not built yet") {
		t.Errorf("Cluster with HTTP: %v, want the refusal", err)
	}
}
