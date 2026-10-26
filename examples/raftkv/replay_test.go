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

// A real run's faults re-made in the simulation: its journal's Script
// (crashes, pauses and isolates by qenv's names, each after the steps the
// nodes had recorded) as the chooser of the simulated cluster, with
// qenv's pause and cut lasting as the real ones did. Every fault the
// script names happens (qnemesis.Unmade), and the cluster's
// model judges the run as the real one's. RAFTKV_REAL_JOURNAL replays a
// failed run's journal (its log names it); else a real run makes one.
func TestRealFaultsInSimulation(t *testing.T) {
	qtest.RequireQuint(t)
	path := os.Getenv("RAFTKV_REAL_JOURNAL")
	if path == "" {
		if os.Getenv("RAFTKV_REAL") != "1" {
			t.Skip("RAFTKV_REAL=1 makes a real run's journal to replay; RAFTKV_REAL_JOURNAL=<journal.jsonl> replays one")
		}
		url := toxiproxyServer(t)
		dir := t.TempDir()
		// seed 5's schedule: a pause, a crash, an isolate
		v := runUnderNemesis(t, buildNode(t, dir), url, filepath.Join(dir, "run"), realConfig(5, Faithful))
		path = filepath.Join(v.Dir, "journal.jsonl")
	}
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
		t.Logf("not as it happened: %s", u)
	}
	cfg := Config{Record: Cluster, Load: true, Partitions: true, Stalls: qenv.Stalls{Pause: 300 * time.Millisecond}, CutLength: j.LongestCut()}
	sys := system(cfg)
	sys.Run = func(c qenv.Chooser) error { return Run(c, cfg) }
	r := sys.Judge(t, sc)
	// every fault made, a heal included (CutLength keeps each cut until
	// its heal); else why not, the root cause first
	if err := qnemesis.Unmade(sc); err != nil {
		t.Error(err)
	}
	r.Expect(t, qtest.Conforms())
	t.Logf("re-made %d faults: %v", len(sc.Faults)-len(sc.Remaining()), sc.Faults)
}
