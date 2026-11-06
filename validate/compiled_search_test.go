package validate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func compiledHistoryStep(i int, action string, amount, balance int64) qtrace.Step {
	base := time.Unix(10, 0)
	rec := fmt.Sprint("client", i)
	return qtrace.Step{Actor: "account", Recorder: rec, Thread: rec, Process: "p", Seq: 1, Action: action,
		Invoked: base, Time: base.Add(time.Duration(i+1) * time.Second), Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
}

func TestCompiledLinearization(t *testing.T) {
	skip.Quint(t)
	bp, cp := compiledCopy(t, "account", "account.qnt")
	t.Setenv("PATH", t.TempDir()) // validating must never rediscover Quint or Node
	for _, mode := range []string{"go", "quint", "shadow"} {
		t.Run(mode, func(t *testing.T) {
			c, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir(), Eval: mode})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			steps := []qtrace.Step{compiledHistoryStep(0, "withdraw", 5, 0), compiledHistoryStep(1, "deposit", 5, 5)}
			recorded, err := c.Check(t.Context(), steps)
			if err != nil || recorded.Receipt().Status != receipt.Fail {
				t.Fatalf("recorded: %v %v", recorded, err)
			}
			j, err := c.Judge(t.Context(), steps, SearchOptions{Linearize: true, Budget: 20})
			if err != nil {
				t.Fatal(err)
			}
			r := j.Report.Receipt()
			if r.Status != receipt.Pass || len(r.Actors) != 1 || r.Actors[0].JudgedBy != "evaluator" || r.Actors[0].Ordering != "searched" || r.Actors[0].Search.OrderBudget != 20 {
				t.Fatalf("receipt: %+v", r)
			}
			if j.Steps[0].Action != "deposit" || j.Report.Actors[0].compiledHash == "" {
				t.Fatalf("order/provenance: %+v", j)
			}
			bad := slices.Clone(steps)
			bad[0].Obs = map[string]any{"balance": int64(99)}
			j, err = c.Judge(t.Context(), bad, SearchOptions{Linearize: true, Budget: 20})
			if err != nil || j.Report.Receipt().Status != receipt.Fail {
				t.Fatalf("exhaustive failure: %+v %v", j, err)
			}
			j, err = c.Judge(t.Context(), steps, SearchOptions{ResolvePending: true})
			if err != nil || j == nil || j.Report.Receipt().Status != receipt.Fail {
				t.Fatalf("recorded-order resolution must retain its failure: %+v %v", j, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			j, err = c.Judge(ctx, steps, SearchOptions{Linearize: true})
			if j != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %+v %v", j, err)
			}
			j, err = c.Judge(t.Context(), steps, SearchOptions{Linearize: true})
			if err != nil || j.Report.Receipt().Status != receipt.Pass {
				t.Fatalf("reuse after cancellation: %+v %v", j, err)
			}
		})
	}
}

func TestCompiledLinearizationBudgetAndKey(t *testing.T) {
	skip.Quint(t)
	bp, cp := compiledCopy(t, "blind", "account.qnt")
	c, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Setenv("PATH", t.TempDir())
	var steps []qtrace.Step
	for i, a := range []int64{1, 2, 4} {
		steps = append(steps, compiledHistoryStep(i, "deposit", a, 0))
	}
	w := compiledHistoryStep(3, "withdraw", 7, 99)
	w.Invoked = time.Unix(20, 0)
	w.Time = time.Unix(21, 0)
	steps = append(steps, w)
	j, err := c.Judge(t.Context(), steps, SearchOptions{Linearize: true, Budget: 2})
	if err != nil || j.Report.Receipt().Status != receipt.Inconclusive {
		t.Fatalf("budget: %+v %v", j, err)
	}
	j, err = c.Judge(t.Context(), steps, SearchOptions{Linearize: true, Budget: 100})
	if err != nil || j.Report.Receipt().Status != receipt.Fail {
		t.Fatalf("complete: %+v %v", j, err)
	}
	steps = nil
	for key := range 2 {
		for i, a := range []int64{1, 2} {
			s := compiledHistoryStep(key*2+i, "deposit", a, 0)
			s.Args["key"] = key
			steps = append(steps, s)
		}
	}
	j, err = c.Judge(t.Context(), steps, SearchOptions{Linearize: true, Key: "key", Budget: 20})
	if err != nil || j.Report.Receipt().Status != receipt.Pass || len(j.Report.Actors) != 2 {
		t.Fatalf("key: %+v %v", j, err)
	}
}

func TestCompiledLinearizationInvariantAlternative(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	b, err := os.ReadFile("testdata/account.yaml")
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.ReplaceAll(string(b), "    expect: 'balance == {{.obs.balance}}'\n", ""))
	bp, cp := filepath.Join(dir, "account.yaml"), filepath.Join(dir, "account.model.json")
	if err := os.WriteFile(bp, b, 0600); err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile("testdata/account.qnt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "account.qnt"), spec, 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := Compile(bp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.Write(cp); err != nil {
		t.Fatal(err)
	}
	c, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Setenv("PATH", t.TempDir())
	steps := []qtrace.Step{compiledHistoryStep(0, "deposit", 80, 0), compiledHistoryStep(1, "deposit", 30, 0), compiledHistoryStep(2, "withdraw", 20, 0)}
	r, err := c.Check(t.Context(), steps)
	if err != nil || r.Receipt().Status != receipt.Fail {
		t.Fatalf("recorded invariant: %+v %v", r, err)
	}
	j, err := c.Judge(t.Context(), steps, SearchOptions{Linearize: true, Budget: 100})
	if err != nil || j.Report.Receipt().Status != receipt.Pass {
		t.Fatalf("invariant-preserving alternative: %+v %v", j, err)
	}
	j, err = c.Judge(t.Context(), steps[:2], SearchOptions{Linearize: true, Budget: 100})
	if err != nil || j.Report.Receipt().Status != receipt.Fail {
		t.Fatalf("all orders violate invariant: %+v %v", j, err)
	}
}

func TestCompiledLinearizationUnsupported(t *testing.T) {
	skip.Quint(t)
	bp, cp := compiledCopy(t, "rr", "rr.qnt")
	c, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Setenv("PATH", t.TempDir())
	s := compiledHistoryStep(0, "place", 1, 0)
	s.Obs = map[string]any{"s2": int64(0)}
	second := compiledHistoryStep(1, "place", 1, 0)
	second.Obs = map[string]any{"s2": int64(0)}
	for _, steps := range [][]qtrace.Step{{s}, {s, second}} {
		j, err := c.Judge(t.Context(), steps, SearchOptions{Linearize: true})
		if j != nil || !errors.Is(err, ErrNeedsQuint) {
			t.Fatalf("unsupported: %+v %v", j, err)
		}
	}
}

func TestCompiledLinearizationInitialSnapshot(t *testing.T) {
	skip.Quint(t)
	bp, cp := compiledCopy(t, "snapshot", "account.qnt")
	c, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Setenv("PATH", t.TempDir())
	base := time.Unix(10, 0)
	snapshot := qtrace.Step{Actor: "account", Recorder: "snapshot", Thread: "snapshot", Seq: 1, Action: "restore", Time: base,
		Args: map[string]any{"b": int64(10), "n": int64(1)}}
	steps := []qtrace.Step{snapshot, compiledHistoryStep(0, "withdraw", 5, 10), compiledHistoryStep(1, "deposit", 5, 15)}
	for i := 1; i < len(steps); i++ {
		steps[i].Invoked = base.Add(time.Second)
		steps[i].Time = base.Add(time.Duration(i+2) * time.Second)
		steps[i].Args["key"] = "x"
	}
	for _, key := range []string{"", "key"} {
		j, err := c.Judge(t.Context(), steps, SearchOptions{Linearize: true, Key: key, Budget: 20})
		if err != nil || j.Report.Receipt().Status != receipt.Pass || j.Steps[0].Action != "restore" {
			t.Fatalf("initial snapshot key=%q: %+v %v", key, j, err)
		}
	}
	// A later checkpoint must never be reordered into an initial restore.
	later := snapshot
	later.Recorder = "later"
	later.Time = base.Add(10 * time.Second)
	_, err = c.Judge(t.Context(), append(slices.Clone(steps), later), SearchOptions{Linearize: true})
	if err == nil || !strings.Contains(err.Error(), "snapshot checkpoint") {
		t.Fatalf("later checkpoint: %v", err)
	}
}
