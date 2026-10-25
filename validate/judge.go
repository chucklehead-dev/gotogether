package validate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"sync"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A Judgment is a run's verdict from Judge: its report, marked with what
// its search found, and the order of its steps that was checked.
type Judgment struct {
	Report *Report
	// Steps are the order checked: the run's steps without a search, else
	// the order the search found (each actor's on one thread of its own).
	Steps []qtrace.Step
}

// A BatchCheck checks runs that are independent of each other, a report
// or an error each, as CheckBatch does: JudgeBatch's hook for a caller
// that keeps verdicts (qtest's cache by instance and steps). It must not
// change the reports it returned before.
type BatchCheck func(ctx context.Context, runs [][]qtrace.Step) ([]*Report, []error)

// Judge is steps' verdict against c's binding: ordered by opt's search
// (Linearize, Key, ResolvePending; none: as recorded), checked, the
// orders the Go evaluator found alone confirmed by quint where the check
// fails (and searched again with quint alone where quint disagrees), and
// the report marked with what the search found (its issues, no verdict
// for an actor whose search ran out of budget). An error is the search's,
// the check's or the confirmation's (and its search again with quint
// alone), never a verdict.
//
// Its check is Check's (its failing actors' ITF written unless NoITF).
func (c *Checker) Judge(ctx context.Context, steps []qtrace.Step, opt SearchOptions) (*Judgment, error) {
	js, errs := c.JudgeBatch(ctx, [][]qtrace.Step{steps}, opt, func(ctx context.Context, runs [][]qtrace.Step) ([]*Report, []error) {
		rep, err := c.Check(ctx, runs[0])
		return []*Report{rep}, []error{err}
	})
	return js[0], errs[0]
}

// JudgeBatch is Judge of many independent runs: their searches side by
// side (each under a directory of its own), their orders checked by
// check in one batch (nil: CheckBatch), then the confirmation of each
// run whose search left claims (an actor that passed and was decided
// needs none), and the runs searched again checked in a second batch.
// Each report is marked on a copy: check's are left as they were.
func (c *Checker) JudgeBatch(ctx context.Context, runs [][]qtrace.Step, opt SearchOptions, check BatchCheck) ([]*Judgment, []error) {
	if check == nil {
		check = func(ctx context.Context, runs [][]qtrace.Step) ([]*Report, []error) {
			return c.CheckBatch(ctx, runs, 0)
		}
	}
	js, errs := make([]*Judgment, len(runs)), make([]error, len(runs))
	searches := make([]*search, len(runs))
	searching := opt.Linearize || opt.ResolvePending
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	each := func(idx []int, f func(i int)) {
		var wg sync.WaitGroup
		for _, i := range idx {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				f(i)
			}()
		}
		wg.Wait()
	}
	all := make([]int, len(runs))
	for i := range all {
		all[i] = i
	}
	// a search's directory: the checker's, else its scratch, else (a
	// checker made by hand) one of the batch's own, never the working
	// directory
	base := c.Options
	if searching {
		var err error
		if base, err = c.dirFor(base); err == nil && base.Dir == "" {
			base.Dir, err = os.MkdirTemp("", "gotogether-judge-")
		}
		if err != nil {
			for i := range errs {
				errs[i] = err
			}
			return js, errs
		}
	}
	each(all, func(i int) {
		sc := *c
		if searching {
			o := base
			if len(runs) > 1 {
				o.Dir = filepath.Join(o.Dir, "search", strconv.Itoa(i))
			}
			sc.Options.Dir = o.Dir
		}
		searches[i], errs[i] = sc.search(runs[i], opt, true)
	})
	// checkAll checks the searches' orders of the runs idx, into js
	checkAll := func(idx []int) {
		if len(idx) == 0 {
			return
		}
		orders := make([][]qtrace.Step, len(idx))
		for k, i := range idx {
			orders[k] = searches[i].Steps
		}
		reps, cerrs := check(ctx, orders)
		for k, i := range idx {
			js[i] = nil
			if errs[i] = cerrs[k]; errs[i] == nil {
				js[i] = &Judgment{Report: reps[k], Steps: searches[i].Steps}
			}
		}
	}
	var todo []int
	for i := range runs {
		if errs[i] == nil {
			todo = append(todo, i)
		}
	}
	checkAll(todo)
	// the Go evaluator's orders, confirmed by their runs' reports: an actor
	// that fails where one speaks is checked by quint, and its run is
	// searched with quint alone and checked again when quint rejects it
	var again []int
	var mu sync.Mutex
	todo = slices.DeleteFunc(todo, func(i int) bool { return errs[i] != nil || len(searches[i].claims) == 0 })
	each(todo, func(i int) {
		s, err := searches[i].confirm(js[i].Report)
		if err != nil {
			errs[i], js[i] = err, nil
			return
		}
		if s != nil {
			searches[i] = s
			mu.Lock()
			again = append(again, i)
			mu.Unlock()
		}
	})
	if len(again) > 0 {
		slices.Sort(again)
		checkAll(again)
	}
	for i, j := range js {
		if j != nil {
			j.Report = j.Report.clone()
			searches[i].mark(j.Report)
		}
	}
	return js, errs
}

// clone is r with copies of what a search's mark writes: its issues, its
// actors' results, their issues and invariants' verdicts.
func (r *Report) clone() *Report {
	c := *r
	c.Issues = slices.Clone(r.Issues)
	c.Actors = make([]*Result, len(r.Actors))
	for i, a := range r.Actors {
		b := *a
		b.Issues = slices.Clone(a.Issues)
		b.Invariants = slices.Clone(a.Invariants)
		c.Actors[i] = &b
	}
	return &c
}
