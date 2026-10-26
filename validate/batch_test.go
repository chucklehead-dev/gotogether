package validate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// CheckBatch gives the same verdicts as Check, at the same steps, with far
// fewer quint processes.
func TestCheckBatchMatchesCheck(t *testing.T) {
	t.Parallel()
	needQuint(t)
	c, err := NewChecker("testdata/account.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	rec := func(ops ...any) []qtrace.Step {
		sink := &qobs.MemorySink{}
		r := qobs.NewRecorder(sink, qobs.WithActor("acct"))
		for i := 0; i+2 < len(ops); i += 3 {
			r.Record(context.Background(), ops[i].(string), nil, "amount", ops[i+1], "obs.balance", ops[i+2])
		}
		return sink.Steps()
	}
	var runs [][]qtrace.Step
	n := 24 // several chunks
	if testing.Short() {
		n = 8 // each kind twice: each run is checked alone too, a quint process each
	}
	for i := 0; i < n; i++ {
		switch i % 4 {
		case 0:
			runs = append(runs, rec("deposit", 5, 5, "withdraw", 2, 3, "deposit", i+1, 4+i))
		case 1:
			runs = append(runs, rec("deposit", 5, 5, "withdraw", 9, -4)) // disabled
		case 2:
			runs = append(runs, rec("deposit", 5, 5, "deposit", 5, 11)) // state
		case 3:
			runs = append(runs, rec("deposit", 60, 60, "deposit", 50, 110)) // invariant underLimit
		}
	}
	verdict := func(r *Report) string {
		a := r.Actors[0]
		s := fmt.Sprint(a.Conforms)
		if a.Failure != nil {
			s += fmt.Sprintf(" %s@%d", a.Failure.Kind, a.Failure.Index)
		}
		for _, inv := range a.Invariants {
			s += " " + inv.Name + "=" + string(inv.Verdict)
			if inv.Failure != nil {
				s += fmt.Sprintf("@%d", inv.Failure.Index)
			}
		}
		return s
	}
	t0 := time.Now()
	batch, errs := c.CheckBatch(context.Background(), runs, 0)
	tBatch := time.Since(t0)
	// each run on its own, in parallel: what CheckBatch replaces
	t0 = time.Now()
	par, perrs := make([]*Report, len(runs)), make([]error, len(runs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i := range runs {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			cc := *c
			cc.Options.Dir = filepath.Join(t.TempDir(), fmt.Sprintf("run%d", i))
			par[i], perrs[i] = cc.Check(context.Background(), runs[i])
		})
	}
	wg.Wait()
	tPar := time.Since(t0)
	for i := range runs {
		if errs[i] != nil || perrs[i] != nil {
			t.Fatalf("run %d: %v / %v", i, errs[i], perrs[i])
		}
		if verdict(batch[i]) != verdict(par[i]) {
			t.Errorf("run %d: batch %q, one by one %q", i, verdict(batch[i]), verdict(par[i]))
		}
	}
	// a mismatch says the model's value, in a chunk as alone: the chunk's
	// backend wrote no ITF for a failing test, and the report had only
	// the rendered expect ("balance == 11") over the observation (11)
	wants := []string{"expect:   balance == 11", "binding:  balance == {{.obs.balance}}", "model state after the step:", "balance = 10"}
	if os.Getenv("QUINT_BACKEND") == "typescript" { // the user's choice: no ITF, and the report says so
		wants = []string{"expect:   balance == 11", "binding:  balance == {{.obs.balance}}", "model state: not reported: the TypeScript backend"}
	}
	for _, want := range wants {
		if s := batch[2].Summary(); !strings.Contains(s, want) {
			t.Errorf("the mismatch's report lacks %q:\n%s", want, s)
		}
	}
	t0 = time.Now()
	for _, r := range runs[:6] {
		if _, err := c.Check(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	tSeq := time.Since(t0) * time.Duration(len(runs)) / 6
	t.Logf("%d runs: CheckBatch %v, Check in parallel %v, Check one at a time ~%v", len(runs), tBatch.Round(time.Millisecond),
		tPar.Round(time.Millisecond), tSeq.Round(time.Millisecond))
}

// A checker with no Dir writes each check under a temporary directory of
// its own, which Close removes: each check once made one that nothing
// removed (262 of them after one test run).
func TestCheckerScratchRemovedByClose(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	c, err := NewChecker("testdata/account.yaml", Options{})
	if err != nil {
		t.Fatal(err)
	}
	steps := []qtrace.Step{{Action: "deposit", Recorder: "r", Seq: 1, Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}}}
	var roots []string
	for range 2 {
		rep, err := c.Check(context.Background(), steps)
		if err != nil {
			t.Fatal(err)
		}
		roots = append(roots, filepath.Dir(filepath.Dir(rep.Actors[0].Module)))
	}
	if roots[0] != roots[1] || !strings.Contains(roots[0], "gotogether-") {
		t.Fatalf("two checks' directories under %v, want one root of the checker's", roots)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(roots[0]); !os.IsNotExist(err) {
		t.Errorf("%s left after Close: %v", roots[0], err)
	}
}

// quint test processes are bounded across the whole process, not per
// CheckBatch: many batches at once (parallel tests' explorations) run at
// most GOMAXPROCS of them. A fake quint marks itself running in a
// directory while it sleeps and logs how many it saw.
func TestQuintProcessesBoundedProcessWide(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "quint")
	script := "#!/bin/sh\nd=" + dir + "/running\nmkdir -p $d\ntouch $d/$$\nls $d | wc -l >> " + dir + "/seen\nsleep 0.2\nrm $d/$$\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	limit := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	for range 3 * limit {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := quintTest(context.Background(), filepath.Join(dir, "m.qnt"), "m", "b", Options{Quint: fake, Backend: "rust", Dir: dir, NoITF: true}, []string{"t"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	b, err := os.ReadFile(filepath.Join(dir, "seen"))
	if err != nil {
		t.Fatal(err)
	}
	most := 0
	for _, f := range strings.Fields(string(b)) {
		var n int
		fmt.Sscan(f, &n)
		most = max(most, n)
	}
	if most == 0 || most > limit {
		t.Errorf("at most %d quint processes at once, want 1..%d (GOMAXPROCS)", most, limit)
	}
}
