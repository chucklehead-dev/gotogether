package qtest

import (
	"context"
	"fmt"
	"slices"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// judgeRecords is how many records (steps, invocation and effect
// records) the runs of an exploration made and not yet judged may hold
// before they are judged, and the passing ones' records let go of
// (DESIGN §112, the batches): an exploration holds its runs' records
// until they are judged, and judging all at the end held all of them, a
// search's and a plan's for each beside. raftkv's runs, a few thousand
// records each (every double's send is one), took a nightly's 500 seeds
// a test past the runner's 16 GB; a batch of this many is a few dozen of
// them. An exploration whose runs hold fewer is judged once, at the end,
// as before, and keeps every run's records.
const judgeRecords = 1 << 17

// batching is how an exploration's runs are judged while they are made:
// a batch at a time, once the runs made and not judged hold the budget's
// records (judgeBatches).
type batching struct {
	budget int  // judgeRecords, unless a test sets another (judgeEvery)
	keep   bool // KeepRecords: no run's records are let go of
	prog   *progress

	from    int  // x.Runs[from:] are not judged yet
	records int  // the records of those made
	stepped bool // a run made recorded a step: else none is judged before finish, which fails (or skips) an exploration of none
	// off: a run failed to run, or the test failed: finish fails the
	// exploration before judging it, as it did when it judged every run
	// at the end, so no more batches are judged
	off     bool
	batches int    // the batches judged before finish's
	err     string // the first batch's check that failed, for finish to say after the runs' own errors

	determined bool // the first run was made again and compared (checkDeterminism)
	steers     bool // a source steers from the runs made, by their features
	cc         *validate.Checker
	inst       string            // the instance key of cc's verdicts (instanceChecker)
	strings    map[string]string // the released runs' choices' kinds and names, one string each (compact)
}

// full reports whether the runs made and not judged are to be judged
// before more are made.
func (b *batching) full() bool { return !b.off && b.stepped && b.records >= b.budget }

// made counts run r, just made, among the runs not judged.
func (b *batching) made(x *Exploration, r *Run) {
	if failedToRun(x, r) {
		b.off = true
		return
	}
	b.records += len(r.Steps)
	b.stepped = b.stepped || !r.noStep()
}

// failedToRun reports whether finish fails the exploration for run r,
// before judging any run: r is nil (the scenario failed t) or did not
// run, but for a mutant's corpus entry it changed, which its claims
// judge.
func failedToRun(x *Exploration, r *Run) bool {
	return r == nil || r.Err != nil && !(r.origin.kind == "corpus" && r.diverged != nil && x.mutant)
}

// judgeBatches judges the runs made and not judged, x.Runs[from:end],
// while they hold the budget's records: from the first, a batch of the
// runs whose records reach it. The batches are the same every time (a
// run's records are), whichever run ended first.
func (e *engine) judgeBatches(end int) {
	t, x := e.t, e.x
	t.Helper()
	for e.batch.full() {
		e.checkDeterminism() // the first run, made again, before its records go
		if t.Failed() {
			e.batch.off = true
			return
		}
		n, b := 0, e.batch.from
		for b < end && n < e.batch.budget {
			n += len(x.Runs[b].Steps)
			b++
		}
		e.batch.prog.judging.Store(true)
		e.judgeBatch(x.Runs[e.batch.from:b], true)
		e.batch.prog.judging.Store(false)
		e.batch.from, e.batch.records, e.batch.batches = b, e.batch.records-n, e.batch.batches+1
	}
}

// checkDeterminism makes the first run again and compares the two,
// once: before its batch is judged, or after the last run.
func (e *engine) checkDeterminism() {
	if e.batch.determined {
		return
	}
	e.batch.determined = true
	x := e.x
	if len(x.Runs) > 0 && x.Runs[0] != nil && len(e.plans) > 0 && e.plans[0].again != nil {
		first, again := x.Runs[0], e.plans[0].again
		checkDeterminism(e.t, first, func() *Run { return e.one(again(first)) }, x.name(first.origin.seed))
	}
}

// releaseRecords lets go of the records of runs, just judged, that
// passed: their Steps and Recorded, and the effect records their
// report's steps carry (detached). What reads a run after it is judged
// keeps what it needs: the claims, coverage and Evidence read reports
// and choices, which stay; steering reads its features, kept; vacuity
// its witnesses, looked for first (witnessReleased). A failing run keeps
// everything (its report is shown with its step log, replayed, shrunk,
// navigated); a passing run a claim shows is made again from its
// choices (restore).
func (e *engine) releaseRecords(runs []*Run) {
	e.x.witnessReleased(runs)
	for _, r := range runs {
		if r.Err != nil || r.Report == nil || !r.Report.OK() {
			continue
		}
		if e.batch.steers {
			r.feats = features(r)
		}
		r.Steps, r.Recorded = nil, nil
		r.Report = detached(r.Report)
		r.adjusted = r.Report
		r.released = true
		r.Choices = e.batch.compact(r.Choices)
	}
}

// compact is choices as a run keeps them once its records are let go of:
// the same choices, in a slice of their own length (a log's appends left
// room for a quarter more), their kinds and names one string each across
// the exploration (a double's fault kind is concatenated anew at every
// choice it makes). What is left of a released run is mostly its
// choices, which its replay, coverage and claims read: raftkv's runs
// make some forty thousand.
func (b *batching) compact(choices []qenv.Choice) []qenv.Choice {
	if b.strings == nil {
		b.strings = map[string]string{}
	}
	intern := func(s string) string {
		if t, ok := b.strings[s]; ok {
			return t
		}
		b.strings[s] = s
		return s
	}
	out := slices.Clone(choices)
	for i := range out {
		out[i].Kind, out[i].Name = intern(out[i].Kind), intern(out[i].Name)
	}
	return out
}

// restore makes a run whose records were released again from its
// choices (an exploration's runs are deterministic: the first is made
// twice to show it), and judges it again for the order judged (a
// search's), its verdict the cache's: what a passing run a claim shows
// needs to be described with its step log.
func (e *engine) restore(ctx context.Context, r *Run) error {
	again := runOne(e.c, e.woven, qenv.Replay(r.Choices), e.scenario)
	switch {
	case again.diverged != nil:
		return fmt.Errorf("its replay diverged: %v", again.diverged)
	case again.Err != nil:
		return fmt.Errorf("its replay did not run: %v", again.Err)
	case len(again.Choices) != len(r.Choices):
		return fmt.Errorf("its replay made %d choices, not %d", len(again.Choices), len(r.Choices))
	}
	var cached int
	js, errs := e.batch.cc.JudgeBatch(ctx, [][]qtrace.Step{again.Steps}, e.c.searchOptions(), cachedCheck(e.batch.cc, e.batch.inst, &cached))
	if errs[0] != nil {
		return errs[0]
	}
	r.Recorded, r.Steps, r.released = again.Steps, js[0].Steps, false
	return nil
}
