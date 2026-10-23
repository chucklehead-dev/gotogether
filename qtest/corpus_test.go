package qtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A failed claim saves the run it shows to the corpus; the next
// exploration replays it before its seeds, so the failure stays found
// whatever the seeds are.
func TestCorpus(t *testing.T) {
	t.Setenv("GOTOGETHER_KEEP", t.TempDir()) // its failures and runs alone are on purpose: keep nothing after it
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
	// another exploration of the test, sharing the directory, replays none
	// of #1's entries
	other := sys.Explore(t, ExploreConfig{Seeds: 1, Corpus: dir, Chooser: func(uint64) qenv.Chooser { return qenv.Nominal{} }})
	if len(other.Runs) != 1 {
		t.Errorf("exploration #2 replayed another's entries: %d runs", len(other.Runs))
	}
	// the next run of the test: its first exploration replays the entry
	// after seeds that never fail (every choice nominal)
	rerun(t)
	x := sys.Explore(t, ExploreConfig{Seeds: 1, Corpus: dir, Chooser: func(uint64) qenv.Chooser { return qenv.Nominal{} }})
	x.Expect(t, Rejects("withdraw"), Some(Conforms()))
	if len(x.Runs) != 2 || x.Runs[1].Corpus != saved[0] || x.Runs[1].OK() {
		t.Errorf("runs: %d, the corpus run %q OK %v", len(x.Runs), x.Runs[len(x.Runs)-1].Corpus, x.Runs[len(x.Runs)-1].OK())
	}
}

// rerun numbers the test's next exploration #1, as the next go test run
// of the test does.
func rerun(t *testing.T) {
	explorations.mu.Lock()
	delete(explorations.n, t.Name())
	explorations.mu.Unlock()
}

// An entry of another format is refused with the fix; one whose choices
// no longer replay the run (the code changed) is stale, and says how to
// find its run again, rather than "did not run".
func TestCorpusStaleAndForeign(t *testing.T) {
	RequireQuint(t)
	t.Setenv("GOTOGETHER_KEEP", t.TempDir())
	sys := System{Binding: "../validate/testdata/account.yaml", Run: race}
	explore := func(dir string) []string {
		rerun(t)
		f := &failTB{TB: t}
		done := make(chan struct{})
		go func() { //qenv:ok failTB's FailNow ends the goroutine (runtime.Goexit)
			defer close(done)
			sys.Explore(f, ExploreConfig{Seeds: 1, Corpus: dir})
		}()
		<-done
		return f.errs
	}
	write := func(dir, name string, e corpusEntry) {
		b, _ := json.Marshal(e)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	foreign := t.TempDir()
	write(foreign, "old.json", corpusEntry{Exploration: t.Name() + "#1", Seed: 3, Choices: []qenv.Choice{{Kind: "sched", N: 2}}})
	if errs := explore(foreign); len(errs) == 0 || !strings.Contains(errs[0], `has format "", not "gotogether.run/1"`) || !strings.Contains(errs[0], "delete it") {
		t.Errorf("a foreign entry: %q", errs)
	}
	stale := t.TempDir()
	write(stale, "stale.json", corpusEntry{Format: corpusFormat, Exploration: t.Name() + "#1", Seed: 3,
		Choices: []qenv.Choice{{Kind: "fault.no.such.kind", N: 7, Value: 5}}})
	errs := strings.Join(explore(stale), "\n")
	if !strings.Contains(errs, "corpus entry stale.json is stale") || !strings.Contains(errs, "GOTOGETHER_RUN='"+t.Name()+"#1:seed=3'") {
		t.Errorf("a stale entry: %s", errs)
	}
}
