package qtest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/formats"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// A feature is something a run reached, as steering tells runs apart,
// and where: at is how many of the run's choices lead to it (a steered
// run that keeps that many reaches it again, and branches from there),
// -1 when no choice tells (a code block).
type feature struct {
	name string
	at   int
}

// features are what a run reached: each model step's action, outcome and
// observed state (its obs values, the model state the step projects),
// each fault made, each code block run (under qweave -cover), where it
// first reached it. A run with one no earlier run had found something
// new. (Counting them as AFL does, a second reach its own feature, bred
// no more of the journal's rare restart, observed or not: not done.)
func features(r *Run) []feature {
	var fs []feature
	seen := map[string]bool{}
	add := func(name string, at int) {
		if !seen[name] {
			seen[name] = true
			fs = append(fs, feature{name, at})
		}
	}
	// a step is reached by the choices made before the step after it
	recorded := r.Recorded
	if recorded == nil {
		recorded = r.Steps
	}
	model := 0
	for _, s := range recorded {
		if !s.Model() {
			continue
		}
		model++
		obs := make([]string, 0, len(s.Obs))
		for k, v := range s.Obs {
			obs = append(obs, fmt.Sprintf("%s=%v", k, v))
		}
		sort.Strings(obs)
		at := len(r.Choices)
		for j, c := range r.Choices {
			if c.Step >= model {
				at = j
				break
			}
		}
		add(fmt.Sprintf("step %s/%s %s", s.Action, s.Outcome, strings.Join(obs, ",")), at)
	}
	for j, c := range r.Choices {
		if qenv.IsFault(c.Kind) && c.Value != 0 {
			add(fmt.Sprintf("fault %s=%d", c.Kind, c.Value), j+1)
		}
	}
	if r.Code != nil {
		for id := range r.Code.Hits() {
			fs = append(fs, feature{fmt.Sprintf("block %d", id), -1})
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

// Entropy implements qenv.Entropy: the parent's chunks, while the run asks
// the same (gosim's choices but faults are chunks), then the seed's.
func (s *steering) Entropy(n int) []byte {
	if s.i < len(s.prefix) {
		if c := s.prefix[s.i]; c.Kind == "entropy" && c.N == n {
			s.i++
			return c.Bytes
		}
		s.i = len(s.prefix)
	}
	if e := qenv.EntropyOf(s.rest); e != nil {
		return e(n)
	}
	return make([]byte, n)
}

// steerDir is where an exploration's steered runs' choices go: outside
// the test's temporary directory, so a reproduce line still finds them,
// under the package's directory, so two packages' tests of one name do
// not share it.
func steerDir(x *Exploration) string {
	return filepath.Join(os.TempDir(), "gotogether-steered", safeName(pkgDir()), safeName(x.test)+fmt.Sprintf("#%d", x.id))
}

// readEntry reads one saved run (a steered run's file, a corpus entry).
func readEntry(path, ref string) (corpusEntry, error) {
	var e corpusEntry
	b, err := os.ReadFile(path)
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return e, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := formats.Corpus.Check(e.Format); err != nil {
		if formats.Corpus.Is(e.Format) {
			return e, fmt.Errorf("%s: %w", path, err)
		}
		return e, fmt.Errorf("%s has format %q, not %q: written by another version of gotogether", path, e.Format, corpusFormat)
	}
	if e.Exploration != ref {
		return e, fmt.Errorf("%s is a run of %s, not of %s: a saved run replays only in the exploration that made it", path, e.Exploration, ref)
	}
	return e, nil
}
