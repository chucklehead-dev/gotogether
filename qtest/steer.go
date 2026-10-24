package qtest

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// features are what a run reached, as steering tells runs apart: each
// model step's action, outcome and observed state (its obs values, the
// model state the step projects), each fault made, each code block run
// (under qweave -cover). A run with one no earlier run had found
// something new.
func features(r *Run) []string {
	var fs []string
	for _, s := range r.Steps {
		if !s.Model() {
			continue
		}
		obs := make([]string, 0, len(s.Obs))
		for k, v := range s.Obs {
			obs = append(obs, fmt.Sprintf("%s=%v", k, v))
		}
		sort.Strings(obs)
		fs = append(fs, fmt.Sprintf("step %s/%s %s", s.Action, s.Outcome, strings.Join(obs, ",")))
	}
	for _, c := range r.Choices {
		if qenv.IsFault(c.Kind) && c.Value != 0 {
			fs = append(fs, fmt.Sprintf("fault %s=%d", c.Kind, c.Value))
		}
	}
	if r.Code != nil {
		for id := range r.Code.Hits() {
			fs = append(fs, fmt.Sprintf("block %d", id))
		}
	}
	return fs
}

// steering replays a parent run's first choices, as long as the run asks
// the same ones, then makes the rest with its own chooser: a run that
// starts where the parent did and goes elsewhere.
type steering struct {
	prefix []qenv.Choice
	i      int
	rest   qenv.Chooser
}

// Choose implements qenv.Chooser.
func (s *steering) Choose(kind string, n int) int {
	if s.i < len(s.prefix) {
		c := s.prefix[s.i]
		s.i++
		if c.Kind == kind && c.N == n {
			return c.Value
		}
		s.i = len(s.prefix) // the run went elsewhere: the rest is the seed's
	}
	return s.rest.Choose(kind, n)
}

// Unwrap implements qenv.Wrapper: the run's context and Rates are its
// chooser's.
func (s *steering) Unwrap() qenv.Chooser { return s.rest }

// steer runs n more runs after runs (the seeds'), each a run that found a
// feature no earlier run had, replayed up to a random choice and then made
// on a fresh seed (of next, the chooser of a seed). Parents and cuts are
// drawn from a generator of the exploration's id, and each batch's
// features are counted in order, so the steered runs are the same every
// time. Each run's choices are saved to a file in dir, which a reproduce
// line names (GOTOGETHER_RUN=...:choices=<file>).
func (x *Exploration) steer(runs []*Run, n, parallel int, dir string, next func(seed uint64) qenv.Chooser, one func(qenv.Chooser) *Run) ([]*Run, error) {
	seen := map[string]bool{}
	var pool []*Run
	grow := func(r *Run) {
		fresh := false
		for _, f := range features(r) {
			if !seen[f] {
				seen[f], fresh = true, true
			}
		}
		if fresh && len(r.Choices) > 0 {
			pool = append(pool, r)
		}
	}
	for _, r := range runs {
		if r != nil && r.Err == nil {
			grow(r)
		}
	}
	if len(pool) == 0 {
		return nil, nil
	}
	os.RemoveAll(dir) // an earlier exploration's
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	rng := rand.New(rand.NewPCG(uint64(x.id), 0x57ee12ed))
	var out []*Run
	for k := 0; k < n; {
		batch := min(parallel, n-k)
		made := make([]*Run, batch)
		var wg sync.WaitGroup
		for i := range batch {
			parent := pool[rng.IntN(len(pool))]
			cut := rng.IntN(len(parent.Choices) + 1)
			seed := uint64(1)<<40 + uint64(k+i+1)
			wg.Add(1)
			go func() { //qenv:ok steered runs side by side, each its own simulation
				defer wg.Done()
				r := one(&steering{prefix: parent.Choices[:cut], rest: next(seed)})
				r.steered = filepath.Join(dir, fmt.Sprintf("%d.json", k+i+1))
				r.steeredFrom = x.nameOf(parent)
				made[i] = r
			}()
		}
		wg.Wait()
		for _, r := range made {
			b, err := json.Marshal(corpusEntry{Format: corpusFormat, Exploration: x.ref(), Choices: r.Choices})
			if err == nil {
				err = os.WriteFile(r.steered, b, 0o644)
			}
			if err != nil {
				return out, fmt.Errorf("saving steered run %s: %w", r.steered, err)
			}
			if r.Err == nil {
				grow(r)
			}
			out = append(out, r)
		}
		k += batch
	}
	return out, nil
}

// steerDir is where an exploration's steered runs' choices go: outside
// the test's temporary directory, so a reproduce line still finds them.
func steerDir(x *Exploration) string {
	return filepath.Join(os.TempDir(), "gotogether-steered", safeName(x.test)+fmt.Sprintf("#%d", x.id))
}

// readEntry reads one saved run (a steered run's file, a corpus entry).
func readEntry(path string) (corpusEntry, error) {
	var e corpusEntry
	b, err := os.ReadFile(path)
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return e, fmt.Errorf("%s: %w", path, err)
	}
	if e.Format != corpusFormat {
		return e, fmt.Errorf("%s has format %q, not %q: written by another version of gotogether", path, e.Format, corpusFormat)
	}
	return e, nil
}
