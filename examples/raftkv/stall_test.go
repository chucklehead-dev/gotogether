package raftkv

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A host stall in the real tier's terms: a cluster history (the
// simulation's, every node cut off and healed) with every node frozen
// for 1.2s right after the heal, as CI's seed 5 was, so its first leader
// comes over a second after it, past cluster.real.binding.yaml's 500ms.
// Given the stall the harness measured over that window, the run has no
// verdict (host stall): not a pass, not a FAIL. The same history without
// the stall (the harness measuring none: stall detection off), with the
// stall during the cut, before the heal, or with one too short to make
// the leader late by itself, violates the bound. The run's other checks
// stand: electionSafety holds in all. The real tier runs a seed with no
// verdict again (runAgain).
func TestHostStallIsNoVerdict(t *testing.T) {
	qtest.RequireModel(t, "cluster.real.binding.yaml")
	cfg := Config{Record: Cluster, Scenario: partitioned, Duration: 6 * time.Second}
	sim := system(cfg).WithRates(qenv.NoFaults).Judge(t, qenv.NewSeeded(1))
	sim.Expect(t, qtest.Conforms(), qtest.Settles())

	var heal time.Time
	for _, s := range sim.Recorded {
		if s.Action == "heal" {
			heal = s.Time
			break
		}
	}
	if heal.IsZero() {
		t.Fatal("the simulated run recorded no heal")
	}
	const frozen = 1200 * time.Millisecond
	stalled := make([]qtrace.Step, len(sim.Recorded))
	for i, s := range sim.Recorded {
		if s.Time.After(heal) { // every node frozen from the heal on: each step after it later by as much
			s.Time = s.Time.Add(frozen)
		}
		stalled[i] = s
	}
	at := func(from, d time.Duration) qtrace.Stall {
		return qtrace.Stall{Start: heal.Add(from), End: heal.Add(from + d), Kind: "disk", Detail: "a write and fsync took " + d.String()}
	}
	const late = "within 500ms after healed: leaderOrSplit"
	measured := qtest.Validate(t, "cluster.real.binding.yaml", stalled, qtest.HostStalls(at(0, frozen)))
	if !measured.Stalled() {
		t.Errorf("a leader late by the host's stall: want no verdict (host stall), got:\n%s", measured.Report.Summary())
	}
	if qtest.Violates("electionSafety").Holds(measured) || qtest.Violates(late).Holds(measured) {
		t.Errorf("an excused run violates:\n%s", measured.Report.Summary())
	}
	// the same history without the stall: the harness measuring none,
	// stall detection off
	plain := qtest.Validate(t, "cluster.real.binding.yaml", stalled)
	plain.Expect(t, qtest.Violates(late))
	if qtest.Violates("electionSafety").Holds(plain) {
		t.Errorf("electionSafety violated:\n%s", plain.Report.Summary())
	}
	// stalls that do not excuse it, on the same verdict (Validate's
	// HostStalls is ExcuseStalls on its report)
	for _, x := range []struct {
		desc  string
		stall qtrace.Stall
	}{
		{"the stall during the cut, before the heal", at(-2*time.Second, frozen)},
		{"a stall too short to make it late", at(0, 300*time.Millisecond)},
	} {
		r := &qtest.Run{Report: plain.Report.ExcuseStalls([]qtrace.Stall{x.stall})}
		if r.Stalled() || !qtest.Violates(late).Holds(r) {
			t.Errorf("%s: Stalled %v, want %s violated:\n%s", x.desc, r.Stalled(), late, r.Report.Summary())
		}
	}
	if r := (&qtest.Run{Report: plain.Report.ExcuseStalls([]qtrace.Stall{at(0, frozen)})}); !r.Stalled() {
		t.Errorf("ExcuseStalls of the plain report with the stall: not stalled, as Validate's HostStalls is:\n%s", r.Report.Summary())
	}
	runAgain(t, sim.Recorded, measured, plain)
}

// runAgain: how the real tier's claims treat a run with no verdict (host
// stall), on TestHostStallIsNoVerdict's runs (stalled: no verdict; plain:
// the same history violating the bound) and the simulated history
// conforming to cluster.real.binding.yaml (ok). A claim over the stalled
// run alone fails, as PR #284's TestRealNoRequestID did: no verdict is no
// pass. Its seed is run again (untilJudged), at most 1+stallRetries
// times: a run again that conforms judges the seed, and the claim
// holds; one that violates the bound fails it, as a FAIL first would; a
// seed with no verdict in every run fails the claim. A history with a
// verdict keeps it, whatever the other's: a clients' history's FAIL in a
// run whose cluster history stalled is the seed's. A run with a verdict
// is never run again. Each stalled run is a line of inconclusive.txt.
func runAgain(t *testing.T, recorded []qtrace.Step, stalled, plain *qtest.Run) {
	ok := qtest.Validate(t, "cluster.real.binding.yaml", recorded)
	ok.Expect(t, qtest.Conforms())
	const late = "within 500ms after healed: leaderOrSplit"
	// the claim before: the stalled run's cluster history kept, Conforms
	// failing on it
	if msg, failed := expectFails(t, []*qtest.Run{ok, ok, stalled}, qtest.Conforms()); !failed || !strings.Contains(msg, "1 of 3 runs have no verdict (1 has no verdict (host stall))") {
		t.Errorf("Conforms of a stalled run: failed %v, %q; want it failed with no verdict (host stall)", failed, msg)
	}

	// each case: the runs a seed's runs make, in order, the clients'
	// history beside the cluster's (a stand-in: kv and cluster are picked
	// by Stalled alone)
	type attempt struct{ kv, cluster *qtest.Run }
	for _, x := range []struct {
		desc     string
		attempts []attempt
		runs     int        // how many the seed runs
		kv, cl   *qtest.Run // the runs judging the seed
		fails    string     // the cluster's Conforms fails with this; "" holds
	}{
		{"stalled, then conforming", []attempt{{ok, stalled}, {ok, ok}}, 2, ok, ok, ""},
		{"stalled, then violating", []attempt{{ok, stalled}, {ok, plain}}, 2, ok, plain, "do not have it"},
		{"stalled in every run", []attempt{{ok, stalled}, {ok, stalled}, {ok, stalled}, {ok, ok}}, 1 + stallRetries, ok, stalled, "no verdict (host stall)"},
		{"the clients' FAIL kept", []attempt{{plain, stalled}, {ok, ok}}, 2, plain, ok, ""},
		{"a verdict first: not run again", []attempt{{ok, plain}, {ok, ok}}, 1, ok, plain, "do not have it"},
	} {
		dir := t.TempDir()
		var names []string
		s := untilJudged(t, dir, "seed1", func(name string) realVerdicts {
			a := x.attempts[min(len(names), len(x.attempts)-1)]
			names = append(names, name)
			return realVerdicts{KV: a.kv, Cluster: a.cluster, Dir: filepath.Join(dir, name)}
		})
		if len(names) != x.runs || s.kv().KV != x.kv || s.cluster().Cluster != x.cl {
			t.Errorf("%s: %d runs %v, want %d; kv the %s, cluster the %s", x.desc, len(names), names, x.runs, what(s.kv().KV, ok, plain), what(s.cluster().Cluster, ok, plain))
		}
		msg, failed := expectFails(t, clusterOf([]seedRuns{s}), qtest.Conforms())
		if failed != (x.fails != "") || !strings.Contains(msg, x.fails) {
			t.Errorf("%s: Conforms failed %v, %q; want %q", x.desc, failed, msg, x.fails)
		}
		b, _ := os.ReadFile(filepath.Join(dir, "inconclusive.txt"))
		if got, want := strings.Count(string(b), "no verdict (host stall) of the cluster's history"), x.runs; x.cl != stalled && got != want-1 || x.cl == stalled && got != want {
			t.Errorf("%s: inconclusive.txt says %d stalled runs:\n%s", x.desc, got, b)
		}
	}
	if qtest.Violates(late).Holds(stalled) {
		t.Error("a stalled run violates the bound")
	}
}

// what names a run of runAgain's.
func what(r, ok, plain *qtest.Run) string {
	switch r {
	case ok:
		return "conforming run"
	case plain:
		return "violating run"
	}
	return "stalled run"
}

// expectFails runs qtest.Runs(runs...).Expect with claims on a TB that
// records failures, and returns what it said and whether it failed.
func expectFails(t *testing.T, runs []*qtest.Run, claims ...qtest.Claim) (string, bool) {
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { defer close(done); qtest.Runs(runs...).Expect(f, claims...) }() //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
	<-done
	return strings.Join(f.errs, "\n"), f.failed
}

// failTB is a testing.TB that records failures; its Fatalf and FailNow
// end the goroutine (runtime.Goexit), as testing.T's do.
type failTB struct {
	testing.TB
	errs   []string
	failed bool
}

func (f *failTB) Helper() {}
func (f *failTB) Errorf(format string, a ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, a...))
	f.failed = true
}
func (f *failTB) Fatalf(format string, a ...any) { f.Errorf(format, a...); runtime.Goexit() }
func (f *failTB) Fatal(a ...any)                 { f.Errorf("%s", fmt.Sprint(a...)); runtime.Goexit() }
func (f *failTB) FailNow()                       { f.failed = true; runtime.Goexit() }
func (f *failTB) Failed() bool                   { return f.failed }
func (f *failTB) Logf(string, ...any)            {}
