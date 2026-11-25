package raftkv

import (
	"os"
	"path/filepath"
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
// stand: electionSafety holds in all.
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
	// the real tier's claims leave the inconclusive run out, said in
	// inconclusive.txt, and keep the failing one; given only inconclusive
	// runs, they keep them all, for the claims to fail on
	dir := t.TempDir()
	vs := []realVerdicts{{Cluster: measured, Dir: filepath.Join(dir, "seed1")}, {Cluster: plain, Dir: filepath.Join(dir, "seed2")}}
	if got := judged(t, vs); len(got) != 1 || got[0].Cluster != plain {
		t.Errorf("judged: %d runs, want the failing one alone", len(got))
	}
	if b, err := os.ReadFile(filepath.Join(dir, "inconclusive.txt")); err != nil || !strings.Contains(string(b), "seed1: no verdict (host stall)") {
		t.Errorf("inconclusive.txt: %q, %v", b, err)
	}
	if got := judged(t, vs[:1]); len(got) != 1 {
		t.Errorf("judged of inconclusive runs alone: %d runs, want them kept", len(got))
	}
}
