package qtest

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Reproducing one run. A failing run's report names it by its
// exploration, numbered within its test (TestShop#2: the test's second
// exploration), and by its seed, schedule or corpus entry:
//
//	GOTOGETHER_RUN='TestShop#2:seed=17' go test -run '^TestShop$'
//
// runs that run alone in that exploration; the test's other explorations
// run as usual. A run alone can only be judged by the claims that hold of
// every run (Conforms, All, None): the others (some run, a share of runs,
// coverage, a complete search) are about the runs it left out, so they
// are listed as not judged rather than passed or failed.

// explorations numbers each test's explorations in the order they start,
// from 1 in each run of the test (-count, each fuzz input): the count
// ends with the test. Explorations started from goroutines of one test
// are numbered in the order they happen to start.
var explorations struct {
	mu sync.Mutex
	n  map[string]*testRuns
}

type testRuns struct {
	n       int
	matched bool // GOTOGETHER_RUN named one of them
}

// nextExploration numbers t's next exploration. When t ends, a
// GOTOGETHER_RUN that names t but none of its explorations fails it: it
// would otherwise have run every exploration in full, silently.
func nextExploration(t testing.TB) int {
	explorations.mu.Lock()
	defer explorations.mu.Unlock()
	if explorations.n == nil {
		explorations.n = map[string]*testRuns{}
	}
	name := t.Name()
	tr := explorations.n[name]
	if tr == nil {
		tr = &testRuns{}
		explorations.n[name] = tr
		t.Cleanup(func() {
			explorations.mu.Lock()
			defer explorations.mu.Unlock()
			delete(explorations.n, name)
			if v := os.Getenv("GOTOGETHER_RUN"); v != "" && !tr.matched {
				if s, err := parseRunSpec(v); err == nil && strings.HasPrefix(s.ref, name+"#") && !strings.Contains(s.ref[len(name)+1:], "/") {
					t.Errorf("gotogether: GOTOGETHER_RUN names %s, but %s ran %d explorations: the run is not reproduced (the test changed, or the line is from another -count iteration)", s.ref, name, tr.n)
				}
			}
		})
	}
	tr.n++
	return tr.n
}

// matched notes that GOTOGETHER_RUN named one of test's explorations.
func matched(test string) {
	explorations.mu.Lock()
	defer explorations.mu.Unlock()
	if tr := explorations.n[test]; tr != nil {
		tr.matched = true
	}
}

// ref is how a reproduce line names the exploration: "TestShop#2".
func (x *Exploration) ref() string { return fmt.Sprintf("%s#%d", x.test, x.id) }

// A runSpec is GOTOGETHER_RUN parsed: the exploration and the run in it.
type runSpec struct {
	ref   string // "TestShop#2"
	kind  string // "seed", "schedule" or "corpus"
	value string
}

func (s runSpec) String() string { return s.kind + " " + s.value }

// runSpecFor is the run GOTOGETHER_RUN asks this exploration (ref) to run
// alone, if it names it. The variables it replaced fail with the new form.
func runSpecFor(t testing.TB, ref string) (runSpec, bool) {
	t.Helper()
	for _, old := range []string{"GOTOGETHER_SEED", "GOTOGETHER_SCHEDULE"} {
		if v := os.Getenv(old); v != "" {
			t.Fatalf("gotogether: %s=%q is no longer read: a report's reproduce line names the exploration too, e.g. GOTOGETHER_RUN='%s:seed=17' (or :schedule=..., :corpus=...)", old, v, ref)
		}
	}
	v, ok := os.LookupEnv("GOTOGETHER_RUN")
	if !ok {
		return runSpec{}, false
	}
	spec, err := parseRunSpec(v)
	if err != nil {
		t.Fatalf("gotogether: %v", err)
	}
	if spec.ref != ref {
		return spec, false
	}
	matched(t.Name())
	return spec, true
}

func parseRunSpec(v string) (runSpec, error) {
	for _, kind := range []string{"seed", "schedule", "corpus"} {
		if i := strings.LastIndex(v, ":"+kind+"="); i > 0 && strings.Contains(v[:i], "#") {
			s := runSpec{ref: v[:i], kind: kind, value: v[i+len(kind)+2:]}
			if kind == "seed" {
				if _, err := strconv.ParseUint(s.value, 10, 64); err != nil {
					break
				}
			}
			return s, nil
		}
	}
	return runSpec{}, fmt.Errorf("GOTOGETHER_RUN=%q: want <test>#<exploration>:seed=<n>, :schedule=<choices> or :corpus=<file>, as a failing run's report gives it", v)
}

// reproduce is how to run r alone.
func (x *Exploration) reproduce(r *Run) string {
	if x.fuzz { // the run is the fuzz input's, which Go saves and names
		return fmt.Sprintf("go test -run '^%s$' (Go saved the failing input under testdata/fuzz/%s and prints its file)", strings.ReplaceAll(x.test, "/", "$/^"), strings.SplitN(x.test, "/", 2)[0])
	}
	var what string
	switch {
	case r.Corpus != "":
		what = "corpus=" + filepath.Base(r.Corpus)
	case x.search != nil:
		what = "schedule=" + r.Schedule
	default:
		what = fmt.Sprintf("seed=%d", r.Seed)
	}
	return fmt.Sprintf("GOTOGETHER_RUN='%s:%s' go test -run '^%s$'", x.ref(), what, strings.ReplaceAll(x.test, "/", "$/^"))
}

// universal reports whether c holds of every run, so a run alone can be
// judged by it. Claims over a share of runs (Reaches, Takes) are not
// judged, even at InAtLeast(1): they are written about the runs as a
// whole.
func universal(c Claim) bool {
	switch c := c.(type) {
	case Expectation:
		return c.every
	case quantified:
		return c.kind == "all" || c.kind == "none"
	}
	return false
}

// keepFiles copies the exploration's artifacts out of t.TempDir(), which
// the test removes when it ends, into a directory of its own, and points
// out (a report) at the copy. With GOTOGETHER_KEEP or a Dir option they
// are kept already.
func (x *Exploration) keepFiles(out string) string {
	if !x.tempDir || x.dir == "" {
		return out
	}
	x.keptOnce.Do(func() {
		x.kept, _ = os.MkdirTemp("", "gotogether-"+strings.NewReplacer("/", "_", " ", "_", "#", "_").Replace(x.ref())+"-")
	})
	if x.kept == "" || copyTree(x.dir, x.kept) != nil {
		return out
	}
	return strings.ReplaceAll(out, x.dir, x.kept)
}

// copyTree copies the files under src to dst.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		o, err := os.Create(to)
		if err != nil {
			return err
		}
		_, err = io.Copy(o, in)
		if cerr := o.Close(); err == nil {
			err = cerr
		}
		return err
	})
}
