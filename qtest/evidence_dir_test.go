package qtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Under GOTOGETHER_EVIDENCE each exploration writes its evidence as
// <dir>/<test>/x<N>.json, with its format and role; the test's first
// exploration of a run clears what earlier runs wrote, so a run with
// fewer explorations leaves none stale.
func TestEvidenceDir(t *testing.T) {
	RequireQuint(t)
	dir := t.TempDir()
	t.Setenv("GOTOGETHER_EVIDENCE", dir)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: race}
	nominal := ExploreConfig{Seeds: 1, Chooser: func(uint64) qenv.Chooser { return qenv.Nominal{} }}
	sys.Explore(t, nominal)
	mutant := nominal
	mutant.corpusOf = t.Name() + "#1" // as Mutate's mutant explorations are
	sys.Explore(t, mutant)
	replay := nominal
	replay.Replay = true
	sys.Explore(t, replay)
	for n, role := range map[string]string{"x1.json": RoleFaithful, "x2.json": RoleMutant, "x3.json": RoleReplay} {
		e, err := ReadEvidence(filepath.Join(dir, t.Name(), n))
		if err != nil || e.Role != role || e.Format != EvidenceFormat {
			t.Errorf("%s: %+v %v, want role %s", n, e, err, role)
		}
	}
	rerun(t) // the next run of the test, with one exploration
	sys.Explore(t, nominal)
	if _, err := os.Stat(filepath.Join(dir, t.Name(), "x2.json")); !os.IsNotExist(err) {
		t.Errorf("a stale x2.json survived the rerun: %v", err)
	}
	if !IsEvidence(filepath.Join(dir, t.Name(), "x1.json")) {
		t.Error("the rerun's x1.json is not evidence")
	}
}
