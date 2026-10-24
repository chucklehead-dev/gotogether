package qtest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	if e.Format != corpusFormat {
		return e, fmt.Errorf("%s has format %q, not %q: written by another version of gotogether", path, e.Format, corpusFormat)
	}
	if e.Exploration != ref {
		return e, fmt.Errorf("%s is a run of %s, not of %s: a saved run replays only in the exploration that made it", path, e.Exploration, ref)
	}
	return e, nil
}
