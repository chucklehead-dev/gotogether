package qtest

import (
	"encoding/json"
	"errors"
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
	if len(x.Runs) != 2 || x.Runs[1].origin.value != saved[0] || x.Runs[1].OK() {
		t.Errorf("runs: %d, the corpus run %q OK %v", len(x.Runs), x.Runs[len(x.Runs)-1].origin.value, x.Runs[len(x.Runs)-1].OK())
	}
}

// rerun numbers the test's next exploration #1, as the next go test run
// of the test does.
func rerun(t *testing.T) {
	explorations.mu.Lock()
	delete(explorations.n, t.Name())
	delete(explorations.cleared, t.Name())
	delete(explorations.runs, t.Name())
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

// An entry that replays exactly and then fails (a regression the
// scenario reports) did not run, and is not stale; one whose run ends
// before its choices do took another path, and is.
func TestCorpusStaleOnlyOnDivergence(t *testing.T) {
	RequireQuint(t)
	t.Setenv("GOTOGETHER_KEEP", t.TempDir())
	dir := t.TempDir()
	run := func(scenario func(qenv.Chooser) error) string {
		rerun(t)
		f := &failTB{TB: t}
		done := make(chan struct{})
		go func() { //qenv:ok failTB's FailNow ends the goroutine (runtime.Goexit)
			defer close(done)
			System{Binding: "../validate/testdata/account.yaml", Run: scenario}.Explore(f, ExploreConfig{Seeds: 20, Corpus: dir}).Expect(f, Conforms())
		}()
		<-done
		return strings.Join(f.errs, "\n")
	}
	run(race) // saves a failing run
	saved, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(saved) == 0 {
		t.Fatal("nothing saved")
	}
	errs := run(func(c qenv.Chooser) error {
		if err := race(c); err != nil {
			return err
		}
		return errors.New("a regression")
	})
	if !strings.Contains(errs, filepath.Base(saved[0])+" did not run: a regression") || strings.Contains(errs, "is stale") {
		t.Errorf("an exact replay that fails: %s", errs)
	}
	// the entry with choices its run never asks for
	b, _ := os.ReadFile(saved[0])
	var e corpusEntry
	_ = json.Unmarshal(b, &e)
	e.Choices = append(e.Choices, qenv.Choice{Kind: "sched", N: 2, Value: 1})
	b, _ = json.Marshal(e)
	_ = os.WriteFile(saved[0], b, 0o644)
	if errs := run(race); !strings.Contains(errs, "is stale: the run ended after") {
		t.Errorf("a replay that ends early: %s", errs)
	}
}

// An entry an exploration of the test saved, which no exploration of this
// run replayed (the explorations changed), fails the test when it ends,
// rather than stop guarding its regression silently.
func TestCorpusOrphan(t *testing.T) {
	RequireQuint(t)
	t.Setenv("GOTOGETHER_KEEP", t.TempDir())
	dir := t.TempDir()
	f := &failTB{}
	t.Run("x", func(t *testing.T) {
		b, _ := json.Marshal(corpusEntry{Format: corpusFormat, Exploration: t.Name() + "#3", Seed: 3})
		_ = os.WriteFile(filepath.Join(dir, "orphan.json"), b, 0o644)
		f.TB = t
		System{Binding: "../validate/testdata/account.yaml", Run: race}.Explore(f, ExploreConfig{Seeds: 2, Corpus: dir})
	})
	// under -short it is noted, not failed: a full run's exploration the
	// short one never reaches is no orphan
	said := f.errs
	if testing.Short() {
		said = f.logs
	}
	if all := strings.Join(said, "\n"); !strings.Contains(all, "orphan.json was saved by TestCorpusOrphan/x#3, which this run of TestCorpusOrphan/x did not replay (it ran 1 explorations)") {
		t.Errorf("errors: %q, logs: %q", f.errs, f.logs)
	}
}

// A test that failed may not have reached the exploration that saved an
// entry: no orphan is reported then.
func TestCorpusOrphanAfterFailure(t *testing.T) {
	RequireQuint(t)
	t.Setenv("GOTOGETHER_KEEP", t.TempDir())
	dir := t.TempDir()
	f := &failTB{}
	t.Run("x", func(t *testing.T) {
		b, _ := json.Marshal(corpusEntry{Format: corpusFormat, Exploration: t.Name() + "#2", Seed: 3})
		_ = os.WriteFile(filepath.Join(dir, "later.json"), b, 0o644)
		f.TB = t
		System{Binding: "../validate/testdata/account.yaml", Run: race}.Explore(f, ExploreConfig{Seeds: 2, Corpus: dir})
		f.Errorf("the test failed before its second exploration")
	})
	if len(f.errs) != 1 || len(f.logs) > 0 && strings.Contains(strings.Join(f.logs, ""), "later.json") {
		t.Errorf("errors %q, logs %q: want the test's own failure only", f.errs, f.logs)
	}
}
