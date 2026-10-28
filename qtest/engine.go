package qtest

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qcover"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// An engine runs one exploration, whichever method asked for it
// (Explore, Exhaust, Sweep's rows, Mutate's mutants, Fuzz's inputs): its
// sources' runs, side by side, then the stages every exploration's runs
// go through in order (the determinism check of its first run, the names checked against
// the system's declarations, finish's run errors, searches and
// judgment). The methods differ only in their sources.
type engine struct {
	t        testing.TB
	c        settings
	binding  string
	scenario func(qenv.Chooser) error
	parallel int
	woven    bool // the code is woven with qweave -cover
	x        *Exploration
	known    []knownCheck
}

// A knownCheck is names the user gave (Rates keys, Bound.Without kinds)
// to check against the fault kinds the system declared, once the runs
// are over: what says what they are, in the message.
type knownCheck struct {
	what  string
	names []string
}

// A planned run is one run a source asks for.
type planned struct {
	origin  origin
	chooser func() qenv.Chooser // made on the run's goroutine
	// again makes the run once more from the run it made (the determinism
	// check), or is nil: the run is not one to repeat (a fuzz input's, a
	// replay)
	again func(first *Run) qenv.Chooser
	after func(*Run) // fills in what only the finished run tells (a corpus replay's staleness)
}

// A source makes an exploration's runs in batches: next sees every run so
// far (steering grows from them) and returns none when it is done. The
// engine asks each source in turn until it is done.
type source interface {
	next(done []*Run) []planned
}

// begin sets up an exploration of scenario against the binding: its
// options (refusing those an exploration ignores), its number among the
// test's explorations and its directory. The caller builds its sources
// from it (they need the exploration's id and ref) and runs them.
func begin(t testing.TB, binding string, opts []Option, scenario func(qenv.Chooser) error, parallel int) *engine {
	t.Helper()
	c := config(t, opts)
	c.refuse(t, "an exploration (Explore, Exhaust, Sweep, Mutate, Fuzz, FollowTraces)", exploreIgnores)
	x := &Exploration{test: t.Name(), id: nextExploration(t), tempDir: c.tempDir, mutation: c.mutant}
	x.dir = exploreDir(t, c.Dir, x.id)
	return &engine{t: t, c: c, binding: binding, scenario: scenario, parallel: parallel, woven: len(qcover.Blocks()) > 0, x: x}
}

// one runs the scenario once on ch.
func (e *engine) one(ch qenv.Chooser) *Run { return runOne(e.c, e.woven, ch, e.scenario) }

// run runs the sources' runs, in order and e.parallel at a time, and puts
// them through the stages. Runs are written by index, so their order is
// the same every time. A nil run (the scenario failed t from its
// goroutine) ends every source: finish says so.
func (e *engine) run(sources ...source) {
	t, x := e.t, e.x
	t.Helper()
	defer hooks.Strict()() // a step with nowhere to go fails the exploration, rather than vanish from its run
	if x.navigating() {    // GOTOGETHER_NAV: the run alone's node, not an exploration
		x.navigated = true
		e.navigate(sources)
		return
	}
	mark, _ := hooks.Escaped(0)
	var plans []planned
	var planned atomic.Int64
	prog := startProgress(x.ref(), func() int { return int(planned.Load()) })
	defer prog.end()
	stopped := false
	for _, s := range sources {
		for !stopped {
			batch := s.next(x.Runs)
			if len(batch) == 0 {
				break
			}
			runs := make([]*Run, len(batch))
			planned.Add(int64(len(batch)))
			var wg sync.WaitGroup
			sem := make(chan struct{}, e.parallel)
			for i, p := range batch {
				wg.Add(1)
				sem <- struct{}{}
				go func() { //qenv:ok runs side by side, each its own simulation
					defer func() { <-sem; wg.Done() }()
					r := e.one(p.chooser())
					r.origin = p.origin
					if p.after != nil {
						p.after(r)
					}
					runs[i] = r
					prog.done.Add(1)
				}()
			}
			wg.Wait()
			x.Runs, plans = append(x.Runs, runs...), append(plans, batch...)
			stopped = slices.Contains(runs, nil)
		}
	}
	if len(x.Runs) > 0 && x.Runs[0] != nil && plans[0].again != nil {
		first, again := x.Runs[0], plans[0].again
		checkDeterminism(t, first, func() *Run { return e.one(again(first)) }, x.name(first.origin.seed))
	}
	if ran(x.Runs) { // a run that did not run declared nothing: finish says why, rather than a name be called a misspelling
		for _, k := range e.known {
			unknownKinds(t, k.what, k.names, x.Runs)
		}
	}
	prog.judging.Store(true)
	origins := make([]origin, len(plans))
	for i, p := range plans {
		origins[i] = p.origin
	}
	x.finish(t, e.binding, e.c, mark, origins)
}

// ran reports whether every run ran to its end: only then do the runs'
// fault names say what the system declares.
func ran(runs []*Run) bool {
	for _, r := range runs {
		if r == nil || r.Err != nil {
			return false
		}
	}
	return true
}

// seedSource is an exploration's seeds, one batch: each run's chooser is
// made from its seed.
type seedSource struct {
	seeds   []uint64
	chooser func(seed uint64) qenv.Chooser
	// replayed: a run made again replays its own choices (a factory's
	// choosers may be stateful: a Script it made once), else is made from
	// its seed afresh
	replayed bool
	once     bool // no run made again (a fuzz input's)
	done     bool
}

func (s *seedSource) next([]*Run) []planned {
	if s.done {
		return nil
	}
	s.done = true
	out := make([]planned, len(s.seeds))
	for i, seed := range s.seeds {
		out[i] = planned{origin: origin{kind: "seed", seed: seed}, chooser: func() qenv.Chooser { return s.chooser(seed) }}
		switch {
		case s.once:
		case s.replayed:
			out[i].again = func(first *Run) qenv.Chooser { return qenv.Replay(first.Choices) }
		default:
			out[i].again = func(*Run) qenv.Chooser { return s.chooser(seed) }
		}
	}
	return out
}

// topUpSource runs a short exploration's (ShortSeeds under -short) next
// seeds, a batch at a time, while a fault kind its Rates set was reached
// by some run and faulted by none, up to its full Seeds: a claim that the
// faults happened (Reaches(k).Faulted()) then fails under -short only
// where it would in full, not by three seeds' chance (a grist app's
// commit faults, missed by 3 seeds after an unrelated change).
type topUpSource struct {
	from, to uint64 // the seeds left, from..to
	batch    int
	kinds    []string // the Rates' keys set apart from DefaultRates
	chooser  func(seed uint64) qenv.Chooser
}

func (s *topUpSource) next(runs []*Run) []planned {
	if s.from > s.to || !unfaultedKind(runs, s.kinds) {
		return nil
	}
	var out []planned
	for ; s.from <= s.to && len(out) < max(s.batch, 1); s.from++ {
		seed := s.from
		out = append(out, planned{origin: origin{kind: "seed", seed: seed}, chooser: func() qenv.Chooser { return s.chooser(seed) },
			again: func(*Run) qenv.Chooser { return s.chooser(seed) }})
	}
	return out
}

// unfaultedKind reports whether one of kinds (or a kind below one, at a
// dot) was reached by a run of runs and faulted by none.
func unfaultedKind(runs []*Run, kinds []string) bool {
	var cs [][]qenv.Choice
	for _, r := range runs {
		if r != nil {
			cs = append(cs, r.Choices)
		}
	}
	cov := qenv.CoverageOf(cs)
	for _, k := range kinds {
		reached, faulted := false, false
		for _, p := range cov.Points {
			if p.Kind == k || strings.HasPrefix(p.Kind, k+".") {
				reached = reached || p.Runs > 0
				faulted = faulted || p.Faulted() > 0
			}
		}
		if reached && !faulted {
			return true
		}
	}
	return false
}

// setKinds are the fault kinds r sets a rate above 0 for, apart from
// DefaultRates' own (its nemesis rate, which every exploration has).
func setKinds(r qenv.Rates) []string {
	var out []string
	for k, p := range r.Kinds {
		if d, ok := qenv.DefaultRates.Kinds[k]; p > 0 && (!ok || d != p) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// corpusSource replays an exploration's corpus entries, one batch, after
// its seeds (their numbers stay the same). An entry whose run took
// another, shorter path to a clean end no longer reaches what it saved:
// it is stale. One that failed or panicked first ended by its failure,
// which is the entry's run not running, not staleness.
type corpusSource struct {
	paths   []string
	entries []corpusEntry
	done    bool
}

func (s *corpusSource) next([]*Run) []planned {
	if s.done {
		return nil
	}
	s.done = true
	out := make([]planned, len(s.entries))
	for k, e := range s.entries {
		var rp *qenv.Replayer
		out[k] = planned{
			origin:  origin{kind: "corpus", value: s.paths[k], seed: e.Seed},
			chooser: func() qenv.Chooser { rp = qenv.Replay(e.Choices); return rp },
			after: func(r *Run) {
				if n := len(e.Choices); r.diverged == nil && r.Err == nil && rp.Left() > 0 {
					r.diverged = fmt.Errorf("the run ended after %d of the entry's %d choices", n-rp.Left(), n)
					r.Err = r.diverged
				}
			},
		}
	}
	return out
}

// fileSource is one saved run's choices replayed (GOTOGETHER_RUN's
// choices=<file>: a steered run alone).
type fileSource struct {
	path    string
	choices []qenv.Choice
	done    bool
}

func (s *fileSource) next([]*Run) []planned {
	if s.done {
		return nil
	}
	s.done = true
	return []planned{{origin: origin{kind: "choices", value: s.path}, chooser: func() qenv.Chooser { return qenv.Replay(s.choices) }}}
}

// steerSource runs n more runs after the runs before it (the seeds' and
// the corpus's), each a run that found a feature no earlier run had,
// replayed up to a random choice from where it first reached one (so the
// branch keeps what was new) and then made on a fresh seed (of seeded). Parents and cuts are drawn from a generator of the
// exploration's id, and each batch's features are counted in order, so
// the steered runs are the same every time. Each run's choices are saved
// to a file in dir, which a reproduce line names
// (GOTOGETHER_RUN=...:choices=<file>).
type steerSource struct {
	t           testing.TB
	x           *Exploration
	n, parallel int
	dir         string
	seeded      func(seed uint64) qenv.Chooser

	started bool
	seen    map[string]bool
	pool    []parent
	rng     *rand.Rand
	k, last int // runs planned so far, and in the last batch
}

// A parent is a run steering branches from, at or after at: the choices
// that led to its first new feature (-1: anywhere).
type parent struct {
	run *Run
	at  int
}

// grow adds r to the pool if it found a feature no earlier run had.
func (s *steerSource) grow(r *Run) {
	fresh, at := false, -1
	for _, f := range features(r) {
		if !s.seen[f.name] {
			s.seen[f.name], fresh = true, true
			if f.at >= 0 && (at < 0 || f.at < at) {
				at = f.at
			}
		}
	}
	if fresh && len(r.Choices) > 0 {
		s.pool = append(s.pool, parent{r, at})
	}
}

func (s *steerSource) next(done []*Run) []planned {
	if !s.started {
		s.started, s.seen = true, map[string]bool{}
		for _, r := range done {
			if r != nil && r.Err == nil {
				s.grow(r)
			}
		}
		if len(s.pool) == 0 {
			return nil
		}
		os.RemoveAll(s.dir) // an earlier exploration's
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			s.t.Fatalf("gotogether: %v", err)
		}
		s.rng = rand.New(rand.NewPCG(uint64(s.x.id), 0x57ee12ed))
	} else {
		// the last batch's runs, saved and counted in order
		for _, r := range done[len(done)-s.last:] {
			b, err := json.Marshal(corpusEntry{Format: corpusFormat, Exploration: s.x.ref(), Choices: r.Choices})
			if err == nil {
				err = os.WriteFile(r.origin.value, b, 0o644)
			}
			if err != nil {
				s.t.Fatalf("gotogether: saving steered run %s: %v", r.origin.value, err)
			}
			if r.Err == nil {
				s.grow(r)
			}
		}
	}
	if s.k >= s.n {
		return nil
	}
	batch := min(s.parallel, s.n-s.k)
	out := make([]planned, batch)
	for i := range batch {
		p := s.pool[s.rng.IntN(len(s.pool))]
		parent, from := p.run, max(p.at, 0)
		cut := from + s.rng.IntN(len(parent.Choices)-from+1)
		seed := uint64(1)<<40 + uint64(s.k+i+1)
		out[i] = planned{
			origin:  origin{kind: "choices", value: filepath.Join(s.dir, fmt.Sprintf("%d.json", s.k+i+1)), from: s.x.nameOf(parent)},
			chooser: func() qenv.Chooser { return &steering{prefix: parent.Choices[:cut], rest: s.seeded(seed)} },
		}
	}
	s.k, s.last = s.k+batch, batch
	return out
}
