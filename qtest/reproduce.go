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

// explorations numbers each test's explorations in the order they start.
var explorations struct {
	mu sync.Mutex
	n  map[string]int
}

func nextExploration(test string) int {
	explorations.mu.Lock()
	defer explorations.mu.Unlock()
	if explorations.n == nil {
		explorations.n = map[string]int{}
	}
	explorations.n[test]++
	return explorations.n[test]
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
		if v, ok := os.LookupEnv(old); ok {
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
	return spec, spec.ref == ref
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
// judged by it.
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
