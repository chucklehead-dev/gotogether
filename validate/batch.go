package validate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/replay"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// CheckBatch checks many independent runs with as few quint processes as
// possible: each run is reconstructed and planned in Go, the actors' traces
// are grouped by their instance constants (traces of one instance can share
// a module), and each group is replayed by one `quint test` process
// holding a conformance test and invariant tests per trace. Groups run in
// parallel, in chunks of at most perProcess traces (0: about one chunk per
// CPU, at most 64 traces each).
//
// Process start-up and model loading cost ~0.5 s per quint process and
// dominate short replays, so this is much faster than a Check per run
// for many short runs. The verdicts are the same. A chunk quint fails to evaluate is
// re-checked one trace at a time.
func (c *Checker) CheckBatch(ctx context.Context, runs [][]qtrace.Step, perProcess int) (reports []*Report, errs []error) {
	reports, errs = make([]*Report, len(runs)), make([]error, len(runs))
	if len(c.Binding.Hidden) > 0 && c.mode.history == nil {
		// the evaluator's search, run by run: no quint batch to share
		for i, run := range runs {
			if reports[i], errs[i] = c.Check(ctx, run); errs[i] != nil {
				reports[i] = nil
			}
		}
		return reports, errs
	}
	// a directory of its own: under the checker's Dir, else its scratch,
	// else (a checker made by hand) a temporary one
	o, err := c.dirFor(c.Options)
	if err == nil && o.Dir == "" {
		o.Dir, err = os.MkdirTemp("", "gotogether-batch-")
	}
	if err != nil {
		for i := range errs {
			errs[i] = err
		}
		return reports, errs
	}
	base := o.Dir
	opt := c.withinITF(c.Options)
	opt.Dir = base
	// A chunk of several traces is replayed by quint's TypeScript backend
	// unless a backend was chosen: with the Rust backend `quint test`
	// starts one evaluator per test, one after another, each sent the whole
	// module, while TypeScript evaluates them all in one process. Checkout's,
	// glomers' and streams' suites ran 20-55% faster. A single long replay
	// (Validate, a retry here) stays on Rust, which was faster for those.
	chosen := opt.Backend != "" || os.Getenv("QUINT_BACKEND") != ""
	opt, err = opt.resolve()
	if err != nil {
		for i := range errs {
			errs[i] = err
		}
		return reports, errs
	}

	type item struct {
		run   int
		trace *qtrace.Trace
		plan  *binding.Plan
		res   *Result
	}
	groups := map[string][]*item{}
	var keys []string
	for i, steps := range runs {
		traces, issues := qtrace.Reconstruct(steps)
		reports[i] = &Report{Issues: issues}
		for _, t := range traces {
			if c.resumes(t) && c.mode.history == nil {
				// the evaluator's search from the resume's states, confirmed by
				// quint on its own: no batch to share
				r, err := c.checkHidden(ctx, t)
				if err != nil {
					errs[i] = fmt.Errorf("actor %s: %w", t.Actor, err)
					break
				}
				judgeLost(c.Binding, r, t.Lost)
				judgeAbsent(c.Binding, r, t.Lost, t.Gaps, nil, nil)
				reports[i].Actors = append(reports[i].Actors, r)
				continue
			}
			whole := t
			if c.mode.history != nil { // a search's: its candidates are one actor's
				whole = c.mode.history
			}
			p, err := c.plan(t, whole)
			if err != nil {
				errs[i] = fmt.Errorf("actor %s: %w", t.Actor, err)
				break
			}
			it := &item{run: i, trace: t, plan: p, res: newResult(c.Binding, t, p)}
			reports[i].Actors = append(reports[i].Actors, it.res)
			k := strings.Join(p.ConstantList(), ", ")
			if _, ok := groups[k]; !ok {
				keys = append(keys, k)
			}
			groups[k] = append(groups[k], it)
		}
	}
	total := 0
	for _, k := range keys {
		total += len(groups[k])
	}
	if perProcess <= 0 {
		perProcess = (total + runtime.GOMAXPROCS(0) - 1) / runtime.GOMAXPROCS(0)
		perProcess = max(1, min(perProcess, 64))
	}
	type chunk struct {
		n     int
		items []*item
	}
	var chunks []chunk
	for _, k := range keys {
		g := groups[k]
		for s := 0; s < len(g); s += perProcess {
			chunks = append(chunks, chunk{len(chunks), g[s:min(s+perProcess, len(g))]})
		}
	}
	var mu sync.Mutex
	setErr := func(run int, err error) {
		mu.Lock()
		if errs[run] == nil {
			errs[run] = err
		}
		mu.Unlock()
	}
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	for _, ch := range chunks {
		wg.Add(1)
		go func(ch chunk) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			name := fmt.Sprintf("batch%d", ch.n)
			ms := replay.Spec(c.Binding, ch.items[0].plan)
			ms.Header = []string{fmt.Sprintf("%d traces of instance %s", len(ch.items), strings.Join(ms.Constants, ", "))}
			calls := make([][]binding.Call, len(ch.items))
			starts := make([]*binding.Call, len(ch.items))
			results := make([]*Result, len(ch.items))
			for i, it := range ch.items {
				calls[i], starts[i] = it.plan.Calls, it.plan.Start
				results[i] = it.res
			}
			copt := opt
			if !chosen && len(ch.items) > 1 {
				copt.Backend, copt.detailBackend = "typescript", "rust"
			}
			for i, err := range evaluate(ctx, ms, calls, starts, name, copt, results) {
				if err == nil {
					continue
				}
				// quint could not evaluate this trace's tests (a ToolError),
				// or did not report them: re-check it alone, so a problem
				// in one trace, or in the chunk's size, costs no verdicts.
				it := ch.items[i]
				r, err := validatePlan(ctx, c.Binding, it.trace, it.plan, Options{Quint: opt.Quint, Backend: opt.Backend, NoITF: opt.NoITF,
					Dir: filepath.Join(opt.Dir, fmt.Sprintf("%s_retry%d", name, i))})
				if err != nil {
					setErr(it.run, err)
					continue
				}
				*it.res = *r
			}
		}(ch)
	}
	wg.Wait()
	for _, ch := range chunks {
		for _, it := range ch.items {
			if errs[it.run] == nil {
				c.judgeWithin(it.trace, it.res)
				judgeLost(c.Binding, it.res, it.trace.Lost)
				judgeAbsent(c.Binding, it.res, it.trace.Lost, it.trace.Gaps, nil, nil)
			}
		}
	}
	for i, err := range c.shadowRuns(runs) {
		if errs[i] == nil {
			errs[i] = err
		}
	}
	for i := range reports {
		if errs[i] != nil {
			reports[i] = nil
		}
	}
	return reports, errs
}
