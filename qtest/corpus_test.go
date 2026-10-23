package qtest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A failed claim saves the run it shows to the corpus; the next
// exploration replays it before its seeds, so the failure stays found
// whatever the seeds are.
func TestCorpus(t *testing.T) {
	RequireQuint(t)
	dir := t.TempDir()
	sys := System{Binding: "../validate/testdata/account.yaml", Run: race}
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's FailNow ends the goroutine (runtime.Goexit)
		defer close(done)
		sys.Explore(f, ExploreConfig{Seeds: 20, Corpus: dir}).Expect(f, Conforms())
	}()
	<-done
	if !f.failed || !strings.Contains(strings.Join(f.errs, "\n"), "saved to the corpus") {
		t.Fatalf("the failing run was not saved: %q", f.errs)
	}
	saved, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(saved) != 1 {
		t.Fatalf("corpus: %v", saved)
	}
	// seeds that never fail (every choice nominal), and the corpus entry
	x := sys.Explore(t, ExploreConfig{Seeds: 1, Corpus: dir, Chooser: func(uint64) qenv.Chooser { return qenv.Nominal{} }})
	x.Expect(t, Rejects("withdraw"), Some(Conforms()))
	if len(x.Runs) != 2 || x.Runs[1].Corpus != saved[0] || x.Runs[1].OK() {
		t.Errorf("runs: %d, the corpus run %q OK %v", len(x.Runs), x.Runs[len(x.Runs)-1].Corpus, x.Runs[len(x.Runs)-1].OK())
	}
}
