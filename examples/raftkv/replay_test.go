package raftkv

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qnemesis"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// Real runs' faults re-made in the simulation: each journal's Script
// (crashes, pauses and isolates by qenv's names, in the order they
// happened) as the chooser of the simulated cluster, with qenv's pause
// and cut lasting as the real ones did. The real run conforms (both
// histories), every fault its script names happens in the simulation
// (qnemesis.Unmade), and the cluster's model judges the simulated run as
// it did the real one. RAFTKV_REAL_JOURNAL replays a failed run's
// journal (its log names it); else the per-PR seeds' real runs make
// theirs, a crash in each, seed 9's last fault a crash.
func TestRealFaultsInSimulation(t *testing.T) {
	qtest.RequireQuint(t)
	var journals []string
	if path := os.Getenv("RAFTKV_REAL_JOURNAL"); path != "" {
		journals = []string{path}
	} else {
		vs := realRuns(t, prSeeds, func(seed int64) nemesisRun { return realConfig(seed, Faithful) })
		qtest.Runs(kvOf(vs)...).Expect(t, qtest.Conforms())
		qtest.Runs(clusterOf(vs)...).Expect(t, qtest.Conforms())
		for _, v := range vs {
			journals = append(journals, filepath.Join(v.Dir, "journal.jsonl"))
		}
	}
	for _, path := range journals {
		replayJournal(t, path)
	}
}

// replayJournal re-makes the journal at path's faults in a simulated run
// and judges it.
func replayJournal(t *testing.T, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	j, err := qnemesis.ReadJournal(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	sc := j.Script()
	for _, u := range j.Unreproducible() {
		t.Logf("%s: not as it happened: %s", path, u)
	}
	// a cut lasts until its scripted heal: LongestCut is the real one's
	// length, and the simulation's heals come at their first chance after
	// the faults before them, not at the real times, so give it room (at
	// LongestCut alone qenv healed one link of two isolates first, and the
	// scripted heal of it never happened)
	cfg := Config{Record: Cluster, Load: true, Partitions: true, Stalls: qenv.Stalls{Pause: 300 * time.Millisecond}, CutLength: 10 * j.LongestCut()}
	sys := system(cfg)
	sys.Run = func(c qenv.Chooser) error { return Run(c, cfg) }
	r := sys.Judge(t, sc)
	// every fault made, a heal included (CutLength keeps each cut until
	// its heal); else why not, the root cause first
	if err := qnemesis.Unmade(sc); err != nil {
		t.Errorf("%s: %v", path, err)
	}
	r.Expect(t, qtest.Conforms())
	var made []string
	left := map[int]bool{}
	for _, f := range sc.Remaining() {
		for i, g := range sc.Faults {
			if g.Kind == f.Kind && !left[i] {
				left[i] = true
				break
			}
		}
	}
	for i, f := range sc.Faults {
		if !left[i] {
			made = append(made, f.Kind)
		}
	}
	t.Logf("%s: re-made %d of %d faults: %v", path, len(made), len(sc.Faults), made)
}
