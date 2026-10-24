package qtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GOTOGETHER_RUN runs one run of one exploration alone, as its report
// names it: the test's other explorations run all their seeds, and the
// run alone is judged only by the claims about every run; the others
// are named as not judged, not passed or failed.
func TestRunAlone(t *testing.T) {
	t.Setenv("GOTOGETHER_KEEP", t.TempDir()) // its failures and runs alone are on purpose: keep nothing after it
	RequireQuint(t)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: race}
	first := sys.Explore(t, ExploreConfig{Seeds: 20})
	first.Expect(t, Rejects("withdraw"), Some(Conforms()))
	bad := first.Failing()[0]
	if want := fmt.Sprintf("GOTOGETHER_RUN='%s#1:seed=%d'", t.Name(), bad.origin.seed); !strings.Contains(first.reproduce(bad), want) {
		t.Fatalf("reproduce line %q, want %s", first.reproduce(bad), want)
	}

	t.Setenv("GOTOGETHER_RUN", fmt.Sprintf("%s#3:seed=%d", t.Name(), bad.origin.seed))
	other := sys.Explore(t, ExploreConfig{Seeds: 20}) // #2: not named, so every seed
	if len(other.Runs) != 20 {
		t.Errorf("an exploration GOTOGETHER_RUN does not name ran %d runs, want 20", len(other.Runs))
	}
	log := &logTB{TB: t}
	alone := sys.Explore(t, ExploreConfig{Seeds: 20}) // #3, as named
	if len(alone.Runs) != 1 || alone.Runs[0].origin.seed != bad.origin.seed {
		t.Fatalf("ran %d runs (seed %d), want seed %d alone", len(alone.Runs), alone.Runs[0].origin.seed, bad.origin.seed)
	}
	// Some(Conforms()) is about other runs: not judged, and so not failed
	alone.Expect(log, All(Rejects("withdraw")), Some(Conforms()))
	if !strings.Contains(log.logs, "not judged on one run") || !strings.Contains(log.logs, "not judged on one run (about the runs it leaves out): conformance") {
		t.Errorf("logs: %s", log.logs)
	}
}

// logTB keeps what a test logs.
type logTB struct {
	testing.TB
	logs string
}

func (l *logTB) Helper()                      {}
func (l *logTB) Logf(format string, a ...any) { l.logs += fmt.Sprintf(format, a...) + "\n" }

// The variables GOTOGETHER_RUN replaced fail, with the new form.
func TestOldReproduceVariables(t *testing.T) {
	for _, v := range []string{"GOTOGETHER_SEED", "GOTOGETHER_SCHEDULE"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv(v, "3")
			f := &failTB{TB: t}
			done := make(chan struct{})
			go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
				defer close(done)
				explore(f, "../validate/testdata/account.yaml", exploreSpec{ExploreConfig: ExploreConfig{Seeds: 1}}, race)
			}()
			<-done
			if len(f.errs) == 0 || !strings.Contains(f.errs[0], "GOTOGETHER_RUN='") {
				t.Errorf("errors %q", f.errs)
			}
		})
	}
}

// A failing claim's report points at files that outlive the test: its
// t.TempDir() is removed when the test ends, so they are copied out.
func TestFailureFilesKept(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	var errs []string
	var x *Exploration
	t.Run("inner", func(t *testing.T) {
		f := &failTB{TB: t}
		sys := System{Binding: "../validate/testdata/account.yaml", Run: race}
		done := make(chan struct{})
		go func() { //qenv:ok failTB's FailNow ends the goroutine (runtime.Goexit)
			defer close(done)
			x = sys.Explore(f, ExploreConfig{Seeds: 10})
			x.Expect(f, Conforms())
		}()
		<-done
		errs = f.errs
	})
	if x == nil || x.kept == "" || !strings.HasPrefix(filepath.Base(x.kept), "gotogether-") {
		t.Fatalf("no kept directory: %+v", x)
	}
	t.Cleanup(func() { os.RemoveAll(x.kept) })
	all := strings.Join(errs, "\n")
	var paths []string
	for _, l := range strings.Split(all, "\n") {
		for _, k := range []string{"step log: ", "module: "} {
			if i := strings.Index(l, k); i >= 0 {
				paths = append(paths, strings.TrimSpace(l[i+len(k):]))
			}
		}
	}
	if len(paths) < 2 {
		t.Fatalf("no step log or module in:\n%s", all)
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("the report points at %s, gone after the test: %v", p, err)
		}
	}
}

// A test's explorations are numbered from 1 in each run of the test
// (-count, each fuzz input: the count ends with the test), and a
// GOTOGETHER_RUN that names the test but none of its explorations fails
// it rather than run them all in full.
func TestRunNumbering(t *testing.T) {
	RequireQuint(t)
	t.Setenv("GOTOGETHER_KEEP", t.TempDir())
	sys := System{Binding: "../validate/testdata/account.yaml", Run: race}
	t.Run("iter", func(t *testing.T) {
		a := sys.Explore(t, ExploreConfig{Seeds: 2})
		b := sys.Explore(t, ExploreConfig{Seeds: 2})
		if a.ref() != t.Name()+"#1" || b.ref() != t.Name()+"#2" {
			t.Errorf("refs %s, %s", a.ref(), b.ref())
		}
		if a.dir == b.dir { // under GOTOGETHER_KEEP the second overwrote the first's step logs
			t.Errorf("both explorations write to %s", a.dir)
		}
	})
	explorations.mu.Lock()
	_, kept := explorations.n[t.Name()+"/iter"]
	explorations.mu.Unlock()
	if kept {
		t.Error("the count outlived its test: a -count=2 rerun would number from 3")
	}

	// a repeated subtest's run (a#01) is not the first one's (a)
	t.Run("a", func(t *testing.T) { sys.Explore(t, ExploreConfig{Seeds: 2}) })
	t.Setenv("GOTOGETHER_RUN", t.Name()+"/a#01#1:seed=1")
	t.Run("a", func(t *testing.T) {
		if x := sys.Explore(t, ExploreConfig{Seeds: 2}); len(x.Runs) != 1 {
			t.Errorf("a#01 ran %d runs, want seed 1 alone", len(x.Runs))
		}
	})
	t.Run("a", func(t *testing.T) { // a#02: not named, so in full and unfailed
		sys.Explore(t, ExploreConfig{Seeds: 2})
	})
	os.Unsetenv("GOTOGETHER_RUN")

	f := &failTB{}
	t.Run("unmatched", func(t *testing.T) {
		t.Setenv("GOTOGETHER_RUN", t.Name()+"#5:seed=1")
		f.TB = t
		sys.Explore(f, ExploreConfig{Seeds: 2})
	})
	if len(f.errs) == 0 || !strings.Contains(f.errs[0], "but "+t.Name()+"/unmatched ran 1 explorations") {
		t.Errorf("errors %q", f.errs)
	}
}

// refTest takes the exploration's number off a ref, whatever '#' the
// test's own name has: Go suffixes a repeated subtest #01, a fuzz seed
// input is seed#N.
func TestRefTest(t *testing.T) {
	t.Parallel()
	for ref, want := range map[string]string{
		"TestX#2": "TestX", "TestX/a#01#1": "TestX/a#01", "FuzzX/seed#0#3": "FuzzX/seed#0", "TestX": "", "TestX#": "", "TestX#a": "",
	} {
		if got := refTest(ref); got != want {
			t.Errorf("refTest(%q) = %q, want %q", ref, got, want)
		}
	}
}

// A run found with every fault family on (GOTOGETHER_ALL_FAULTS) is
// reproduced so: its reproduce line sets the variable, and its corpus
// entries go apart from those of runs without it, whose choices differ.
func TestAllFaultsReproduced(t *testing.T) {
	t.Setenv("GOTOGETHER_ALL_FAULTS", "1")
	x := &Exploration{test: "TestX", id: 1}
	if got := x.reproduce(&Run{origin: origin{kind: "seed", seed: 3}}); !strings.HasPrefix(got, "GOTOGETHER_ALL_FAULTS=1 GOTOGETHER_RUN='TestX#1:seed=3'") {
		t.Errorf("reproduce line %q", got)
	}
	if got := corpusDir(t, ExploreConfig{Corpus: "testdata/corpus"}); got != filepath.Join("testdata/corpus", "all-faults") {
		t.Errorf("corpus %q", got)
	}
	t.Setenv("GOTOGETHER_ALL_FAULTS", "")
	if got := corpusDir(t, ExploreConfig{Corpus: "testdata/corpus"}); got != "testdata/corpus" {
		t.Errorf("corpus without it %q", got)
	}
}
