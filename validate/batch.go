package validate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/replay"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// chunkSteps is about the steps a quint process replays in the time it
// takes to load the model: a process typechecks the model and the module
// first (counters': 5 of 5.3 CPU s for a trace of 20 steps; three such
// traces in one process 5.4), then evaluates at about 2 ms a step. A batch
// is split across processes only past it, where the parallel evaluation
// saves more wall time than the extra typechecks cost.
const chunkSteps = 1000

// chunkSize is how many of total traces (of steps steps in all) one quint
// process replays: about one chunk per CPU, but no fewer traces than
// chunkSteps' worth, and at most 64 (a module of 64 runs takes quint
// about a gigabyte).
func chunkSize(total, steps, cpus int) int {
	per := (total + cpus - 1) / cpus
	if total > 0 && steps > 0 {
		avg := max(1, steps/total)
		per = min(max(per, chunkSteps/avg), total)
	}
	return max(1, min(per, 64))
}

// CheckBatch checks many independent runs with as few quint processes as
// possible: each run is reconstructed and planned in Go, the actors' traces
// are grouped by their instance constants (traces of one instance can share
// a module), and each group is replayed by one `quint test` process
// holding a conformance test and invariant tests per trace. Groups run in
// parallel, in chunks of at most perProcess traces (0: chunkSize's, about
// one chunk per CPU when the traces are long enough to repay a process).
//
// Process start-up and model loading cost ~0.5 s per quint process and
// dominate short replays, so this is much faster than a Check per run
// for many short runs. The verdicts are the same. A chunk quint fails to evaluate is
// re-checked one trace at a time.
func (c *Checker) CheckBatch(ctx context.Context, runs [][]qtrace.Step, perProcess int) (reports []*Report, errs []error) {
	reports, errs = make([]*Report, len(runs)), make([]error, len(runs))
	if len(c.Binding.Hidden) > 0 && c.mode.history == nil || c.compiled != nil {
		// the evaluator's search, or a compiled model's evaluator, run by
		// run: no quint batch to share
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
	chosen := opt.envBackend() != ""
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
		// the Go evaluator's verdict, if it gave one (judged), and whether
		// it decided the item (a pass, no quint replay)
		ev      runVerdict
		judged  bool
		decided bool
		sampled bool // a pass quint judges too (GOTOGETHER_EVAL_SAMPLE)
	}
	var all []*item
	groups := map[string][]*item{}
	var keys []string
	for i, steps := range runs {
		traces, issues := reconstruct(c.Binding, steps)
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
			all = append(all, it)
			reports[i].Actors = append(reports[i].Actors, it.res)
			k := strings.Join(p.ConstantList(), ", ")
			if _, ok := groups[k]; !ok {
				keys = append(keys, k)
			}
			groups[k] = append(groups[k], it)
		}
	}
	// the Go evaluator first: a plan it passes is decided, with no quint
	// replay (decide.go); one it rejects goes to quint's detail replay
	re := c.runEvaluator()
	if re != nil {
		var wg sync.WaitGroup
		sem := make(chan struct{}, runtime.GOMAXPROCS(0))
		for _, it := range all {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				var err error
				it.sampled = re.takes(it.plan)
				it.ev, err = re.e.judgeRun(c, it.trace, it.plan, !opt.NoITF || re.shadow || it.sampled)
				it.judged = err == nil
				it.sampled = it.sampled && it.judged && it.ev.pass
				if err != nil {
					re.note(fmt.Sprintf("refused %s %s %d calls: %v", c.Binding.SpecPath(), nameOf(it.trace.Actor), len(it.plan.Calls), err))
				}
				if it.judged && it.ev.pass && !re.shadow && !it.sampled {
					it.decided = re.decide(c, it.trace, it.plan, it.ev, it.res, opt, fmt.Sprintf("run%d_%s", it.run, sanitize(it.trace.Actor)))
				}
			}()
		}
		wg.Wait()
	}
	for _, k := range keys {
		groups[k] = slices.DeleteFunc(groups[k], func(it *item) bool { return it.decided })
	}
	total, steps := 0, 0
	for _, k := range keys {
		total += len(groups[k])
		for _, it := range groups[k] {
			steps += len(it.plan.Calls)
		}
	}
	if perProcess <= 0 {
		perProcess = chunkSize(total, steps, runtime.GOMAXPROCS(0))
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
			detail := make([]bool, len(ch.items))
			for i, it := range ch.items {
				calls[i], starts[i] = it.plan.Calls, it.plan.Start
				results[i] = it.res
				detail[i] = it.judged && !it.ev.pass // the evaluator rejected it
			}
			copt := opt
			if !chosen && len(ch.items) > 1 {
				copt.Backend, copt.detailBackend = "typescript", "rust"
			}
			for i, err := range evaluate(ctx, ms, calls, starts, name, copt, results, detail) {
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
	if re != nil {
		// quint's verdicts, before the checks below add to them, against
		// the evaluator's: every one under shadow, a sampled pass's else
		for _, it := range all {
			switch {
			case !it.judged || errs[it.run] != nil:
			case re.shadow:
				if err := re.compare(c, it.trace, it.plan, it.ev, it.res); err != nil {
					setErr(it.run, err)
				}
			case it.sampled:
				re.checkSample(c, it.trace, it.plan, it.ev, it.res)
			}
		}
	}
	for _, it := range all {
		if errs[it.run] == nil {
			c.explainResult(it.plan, it.res, opt)
			c.judgeWithin(it.trace, it.res)
			judgeLost(c.Binding, it.res, it.trace.Lost)
			judgeAbsent(c.Binding, it.res, it.trace.Lost, it.trace.Gaps, nil, nil)
		}
	}
	for i := range reports {
		if errs[i] != nil {
			reports[i] = nil
		}
	}
	return reports, errs
}
