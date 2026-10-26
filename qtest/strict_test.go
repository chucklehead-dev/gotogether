package qtest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// skipTB is a failTB that records a skip instead of skipping the test.
type skipTB struct {
	*failTB
	skipped string
}

func (s *skipTB) Skip(a ...any) { s.skipped = fmt.Sprint(a...); runtime.Goexit() }
func (s *skipTB) Skipf(format string, a ...any) {
	s.skipped = fmt.Sprintf(format, a...)
	runtime.Goexit()
}
func (s *skipTB) SkipNow() { s.skipped = "(SkipNow)"; runtime.Goexit() }

// outcome runs f on a skipTB, as a test would, and says how it ended:
// "skip: why", "fail: why", or "" when it went on.
func outcome(t *testing.T, f func(tb testing.TB)) string {
	s := &skipTB{failTB: &failTB{TB: t}}
	done := make(chan struct{})
	go func() { //qenv:ok skipTB's Skip and Fatal end the goroutine (runtime.Goexit)
		defer close(done)
		f(s)
	}()
	<-done
	switch {
	case s.skipped != "":
		return "skip: " + s.skipped
	case len(s.errs) > 0:
		return "fail: " + strings.Join(s.errs, "; ")
	}
	return ""
}

// GOTOGETHER_STRICT=1 (or true) turns every toolkit skip into a failure
// with the skip's message, saying the variable is set; without it (or 0,
// false) each skips, and another value fails naming it.
// Optional is the same for a test's own skips.
func TestStrictMode(t *testing.T) {
	noQuint := t.TempDir() // a PATH with no quint on it
	empty := func(qenv.Chooser) error { return nil }
	for _, x := range []struct {
		name, says string
		path       string // PATH, if set
		f          func(tb testing.TB)
	}{
		{"RequireQuint", "quint not installed", noQuint, func(tb testing.TB) { RequireQuint(tb) }},
		{"Mutate", "no mutants woven: run with qweave -mutate", "", func(tb testing.TB) {
			System{Binding: "testdata/counter.yaml", Run: empty}.Mutate(tb, ExploreConfig{Seeds: 1})
		}},
		{"Record's SkipIfEmpty", "no steps recorded", "", func(tb testing.TB) {
			Record(tb, "testdata/counter.yaml", func(*qobs.Recorder) {}, SkipIfEmpty())
		}},
		{"Explore's SkipIfEmpty", "no run recorded a step", "", func(tb testing.TB) {
			System{Binding: "testdata/counter.yaml", Run: empty, Opts: []Option{SkipIfEmpty()}}.Explore(tb, ExploreConfig{Seeds: 2})
		}},
		{"Optional", "PG_URL not set", "", func(tb testing.TB) { Optional(tb, "PG_URL not set") }},
	} {
		if x.path != "" {
			t.Setenv("PATH", x.path)
		}
		for _, v := range []struct{ strict, want, also string }{
			{"", "skip: ", ""}, {"0", "skip: ", ""}, {"false", "skip: ", ""},
			{"1", "fail: ", "GOTOGETHER_STRICT is set"}, {"TRUE", "fail: ", "GOTOGETHER_STRICT is set"},
			// a misspelt setting is not silently lenient
			{"yes", "fail: ", `GOTOGETHER_STRICT="yes": set it to 1 (or true)`},
		} {
			t.Setenv("GOTOGETHER_STRICT", v.strict)
			got := outcome(t, x.f)
			if !strings.HasPrefix(got, v.want) || !strings.Contains(got, x.says) || !strings.Contains(got, v.also) {
				t.Errorf("%s, GOTOGETHER_STRICT=%q: %q, want %s%q %q", x.name, v.strict, got, v.want, x.says, v.also)
			}
		}
	}
}

// A test that skips clears the evidence an earlier run of it left in a
// reused GOTOGETHER_EVIDENCE directory: evidence -expect would read it as
// this run's.
func TestSkipClearsOldEvidence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GOTOGETHER_EVIDENCE", root)
	t.Setenv("GOTOGETHER_STRICT", "")
	old := filepath.Join(evidenceDir(t.Name()), "x1.json")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := (&Evidence{Format: EvidenceFormat, Test: t.Name() + "#1"}).WriteJSON(old); err != nil {
		t.Fatal(err)
	}
	if got := outcome(t, func(tb testing.TB) { Optional(tb, "PG_URL not set") }); !strings.HasPrefix(got, "skip: ") {
		t.Fatalf("Optional: %q", got)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the earlier run's evidence is still there (%v)", err)
	}
}
