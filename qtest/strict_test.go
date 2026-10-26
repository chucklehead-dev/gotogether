package qtest

import (
	"fmt"
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

// GOTOGETHER_STRICT=1 turns every toolkit skip into a failure with the
// skip's message, saying the variable is set; without it each skips.
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
		for _, strict := range []string{"", "0", "1"} {
			t.Setenv("GOTOGETHER_STRICT", strict)
			got := outcome(t, x.f)
			want := "skip: "
			if strict == "1" {
				want = "fail: "
			}
			if !strings.HasPrefix(got, want) || !strings.Contains(got, x.says) || strict == "1" && !strings.Contains(got, "GOTOGETHER_STRICT is set") {
				t.Errorf("%s, GOTOGETHER_STRICT=%q: %q, want %s%q", x.name, strict, got, want, x.says)
			}
		}
	}
}
