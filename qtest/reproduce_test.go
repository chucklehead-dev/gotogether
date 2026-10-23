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
	if want := fmt.Sprintf("GOTOGETHER_RUN='%s#1:seed=%d'", t.Name(), bad.Seed); !strings.Contains(first.reproduce(bad), want) {
		t.Fatalf("reproduce line %q, want %s", first.reproduce(bad), want)
	}

	t.Setenv("GOTOGETHER_RUN", fmt.Sprintf("%s#3:seed=%d", t.Name(), bad.Seed))
	other := sys.Explore(t, ExploreConfig{Seeds: 20}) // #2: not named, so every seed
	if len(other.Runs) != 20 {
		t.Errorf("an exploration GOTOGETHER_RUN does not name ran %d runs, want 20", len(other.Runs))
	}
	log := &logTB{TB: t}
	alone := sys.Explore(t, ExploreConfig{Seeds: 20}) // #3, as named
	if len(alone.Runs) != 1 || alone.Runs[0].Seed != bad.Seed {
		t.Fatalf("ran %d runs (seed %d), want seed %d alone", len(alone.Runs), alone.Runs[0].Seed, bad.Seed)
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
				explore(f, "../validate/testdata/account.yaml", ExploreConfig{Seeds: 1}, race)
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
