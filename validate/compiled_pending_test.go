package validate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// Reuse the pending deposit/observed withdrawal from TestReceiptHistorySearch.
// Both checking modes must explain the observation without any external tools.
func TestCompiledPendingResolution(t *testing.T) {
	skip.Quint(t)
	bp, cp := compiledCopy(t, "blind", "account.qnt")
	c, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Setenv("PATH", t.TempDir())
	base := time.Unix(1, 0)
	steps := []qtrace.Step{
		{Recorder: "r", Seq: 1, Actor: "a", Thread: "t", Process: "p", Action: "deposit", Args: map[string]any{"amount": int64(10)}, Time: base, Record: qtrace.Record{Invocation: true}},
		{Recorder: "r", Seq: 2, Actor: "a", Thread: "t", Process: "p", Action: "withdraw", Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}, Time: base.Add(time.Second)},
	}
	for _, linearize := range []bool{false, true} {
		for _, tc := range []struct {
			name    string
			balance int64
			budget  int
			want    receipt.Status
		}{
			{"assumed", 5, 12, receipt.Pass}, {"bounded", 5, 1, receipt.Inconclusive}, {"impossible", 99, 12, receipt.Fail},
		} {
			t.Run(fmt.Sprintf("linearize=%t/%s", linearize, tc.name), func(t *testing.T) {
				steps[1].Obs = map[string]any{"balance": tc.balance}
				j, e := c.Judge(t.Context(), steps, SearchOptions{ResolvePending: true, Linearize: linearize, ResolveBudget: tc.budget, Budget: tc.budget})
				if e != nil {
					t.Fatal(e)
				}
				r := j.Report.Receipt()
				if r.Status != tc.want {
					t.Fatalf("receipt %+v, want %s", r, tc.want)
				}
				if tc.want == receipt.Pass && (len(r.Actors[0].AssumedEffects) != 1 || r.Actors[0].JudgedBy != "evaluator") {
					t.Fatalf("lost native assumed-effect evidence: %+v", r)
				}
			})
		}
	}
	for _, opt := range []SearchOptions{
		{ResolvePending: true, ResolveBudget: -1},
		{ResolvePending: true, Linearize: true, Budget: -1},
	} {
		if j, err := c.Judge(t.Context(), steps, opt); j != nil || err == nil || !strings.Contains(err.Error(), "nonnegative") {
			t.Fatalf("negative budget must not disable the search cap: %+v %v", j, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if j, e := c.Judge(ctx, steps, SearchOptions{ResolvePending: true}); j != nil || !errors.Is(e, context.Canceled) {
		t.Fatalf("cancel: %+v %v", j, e)
	}
}

// Independently enumerate subsets of two pending deposits. Only a subset's
// total can explain the completed withdrawal, regardless of placement order.
func TestCompiledPendingSubsetOracle(t *testing.T) {
	skip.Quint(t)
	bp, cp := compiledCopy(t, "blind", "account.qnt")
	c, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Setenv("PATH", t.TempDir())
	base := time.Unix(1, 0)
	for balance := int64(0); balance <= 5; balance++ {
		steps := []qtrace.Step{
			{Recorder: "p", Seq: 1, Action: "deposit", Time: base, Record: qtrace.Record{Invocation: true}, Args: map[string]any{"amount": int64(2)}},
			{Recorder: "q", Seq: 1, Action: "deposit", Time: base, Record: qtrace.Record{Invocation: true}, Args: map[string]any{"amount": int64(3)}},
			{Recorder: "r", Seq: 1, Action: "withdraw", Time: base.Add(time.Second), Args: map[string]any{"amount": int64(1)}, Obs: map[string]any{"balance": balance}},
		}
		possible := false
		for mask := 0; mask < 4; mask++ {
			total := int64(0)
			if mask&1 != 0 {
				total += 2
			}
			if mask&2 != 0 {
				total += 3
			}
			possible = possible || total >= 1 && total-1 == balance
		}
		for _, linearize := range []bool{false, true} {
			j, e := c.Judge(t.Context(), steps, SearchOptions{ResolvePending: true, Linearize: linearize, ResolveBudget: 100, Budget: 100})
			if e != nil {
				t.Fatal(e)
			}
			got := j.Report.Receipt().Status
			want := receipt.Fail
			if possible {
				want = receipt.Pass
			}
			if got != want {
				t.Fatalf("balance %d linearize=%t: %s, want %s", balance, linearize, got, want)
			}
		}
	}
}

// The first conforming placement may violate an invariant; a later placement
// can preserve it. An incomplete search must not turn that first choice into
// a claimed model defect.
func TestCompiledPendingInvariantAlternative(t *testing.T) {
	skip.Quint(t)
	bp, cp := compiledPendingInvariantCopy(t)
	c, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c, err = c.WithInvariants("underLimit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	base := time.Unix(1, 0)
	steps := []qtrace.Step{
		{Recorder: "r", Seq: 1, Action: "deposit", Time: base, Args: map[string]any{"amount": int64(80)}},
		{Recorder: "r", Seq: 2, Action: "withdraw", Time: base.Add(time.Second), Record: qtrace.Record{Invocation: true}, Args: map[string]any{"amount": int64(20)}},
		{Recorder: "r", Seq: 3, Action: "deposit", Time: base.Add(2 * time.Second), Args: map[string]any{"amount": int64(30)}},
	}
	for _, budget := range []int{1, 12} {
		j, e := c.Judge(t.Context(), steps, SearchOptions{ResolvePending: true, ResolveBudget: budget})
		if e != nil {
			t.Fatal(e)
		}
		want := receipt.Pass
		if budget == 1 {
			want = receipt.Inconclusive
		}
		if j.Report.Receipt().Status != want {
			t.Fatalf("budget %d: %+v", budget, j.Report.Receipt())
		}
		if budget == 12 && len(j.Report.Receipt().Actors[0].AssumedEffects) != 1 {
			t.Fatalf("safe alternative not retained: %+v", j.Report.Receipt())
		}
	}
	steps[0].Args = map[string]any{"amount": int64(110)}
	j, e := c.Judge(t.Context(), steps, SearchOptions{ResolvePending: true, ResolveBudget: 12})
	if e != nil || j.Report.Receipt().Status != receipt.Fail {
		t.Fatalf("every placement violates: %+v %v", j, e)
	}
}

func TestCompiledPendingUnsupported(t *testing.T) {
	skip.Quint(t)
	bp, cp := compiledCopy(t, "rr", "rr.qnt")
	c, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Setenv("PATH", t.TempDir())
	p := compiledHistoryStep(0, "place", 1, 0)
	p.Invocation = true
	p.Obs = nil
	observed := compiledHistoryStep(1, "place", 1, 0)
	observed.Obs = map[string]any{"s2": int64(0)}
	for _, linearize := range []bool{false, true} {
		j, e := c.Judge(t.Context(), []qtrace.Step{p, observed}, SearchOptions{ResolvePending: true, Linearize: linearize})
		if j != nil || !errors.Is(e, ErrNeedsQuint) {
			t.Fatalf("unsupported linearize=%t: %+v %v", linearize, j, e)
		}
	}
}

// A missing result cannot supply an action argument for an assumed effect.
// Keep the replayable no-effect placement, including when it violates an
// invariant; missing binding input is not unsupported evaluator semantics,
// and an unrenderable alternative prevents a negative proof.
func TestCompiledPendingUnreplayableEffect(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	data, err := os.ReadFile("testdata/blind.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := "invariants: [underLimit]\n" + strings.Replace(string(data), "  withdraw:\n    args: { amount: '{{.amount}}' }", "  withdraw:\n    args: { amount: '{{.obs.amount}}' }", 1)
	bp, cp := filepath.Join(dir, "blind.yaml"), filepath.Join(dir, "model.json")
	if err = os.WriteFile(bp, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	model, err := os.ReadFile("testdata/account.qnt")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "account.qnt"), model, 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := Compile(bp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = cm.Write(cp); err != nil {
		t.Fatal(err)
	}
	c, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c, err = c.WithInvariants("underLimit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	base := time.Unix(1, 0)
	for _, last := range []int64{10, 30} {
		steps := []qtrace.Step{
			{Recorder: "r", Seq: 1, Action: "deposit", Time: base, Args: map[string]any{"amount": int64(80)}},
			{Recorder: "r", Seq: 2, Action: "withdraw", Time: base.Add(time.Second), Record: qtrace.Record{Invocation: true}, Args: map[string]any{"amount": int64(20)}},
			{Recorder: "r", Seq: 3, Action: "deposit", Time: base.Add(2 * time.Second), Args: map[string]any{"amount": last}},
		}
		j, e := c.Judge(t.Context(), steps, SearchOptions{ResolvePending: true, ResolveBudget: 12})
		if last == 30 {
			if j != nil || e == nil || errors.Is(e, ErrNeedsQuint) || !strings.Contains(e.Error(), "unreplayable placement") {
				t.Fatalf("missing effect must not prove failure: %+v %v", j, e)
			}
		} else if e != nil || j.Report.Receipt().Status != receipt.Pass {
			t.Fatalf("safe no-effect witness: %+v %v", j, e)
		}
	}
}

func compiledPendingInvariantCopy(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bp, cp := filepath.Join(dir, "blind.yaml"), filepath.Join(dir, "model.json")
	for _, name := range []string{"blind.yaml", "account.qnt"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "blind.yaml" {
			data = append([]byte("invariants: [underLimit]\n"), data...)
		}
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cm, err := Compile(bp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = cm.Write(cp); err != nil {
		t.Fatal(err)
	}
	return bp, cp
}

// Once an invariant-safe placement exists, a later unsupported candidate is
// irrelevant. Exercise the native selection boundary with the account model:
// a known literal is replayable, an unknown constructor is not.
func TestCompiledPendingInvariantStopsAtWitness(t *testing.T) {
	skip.Quint(t)
	bp, cp := compiledPendingInvariantCopy(t)
	c, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	t.Setenv("PATH", t.TempDir())
	good := compiledHistoryStep(0, "deposit", 10, 0)
	bad := good
	bad.Args = map[string]any{"amount": "UNKNOWN_CONSTRUCTOR"}
	j, err := c.Judge(t.Context(), []qtrace.Step{bad}, SearchOptions{ResolvePending: true})
	if j != nil || !errors.Is(err, ErrNeedsQuint) {
		t.Fatalf("unsupported control: %+v %v", j, err)
	}
	traces, _ := reconstruct(c.Binding, []qtrace.Step{good})
	if len(traces) != 1 {
		t.Fatalf("traces: %d", len(traces))
	}
	fs, errs := compiledResolutions(inHistory(c, traces[0]), traces[0], good.Actor, [][]qtrace.Step{{good}, {bad}}, true)
	if len(fs) != 1 || fs[0] >= 0 || len(errs) != 1 || errs[0] != nil {
		t.Fatalf("safe prefix: %v, %v", fs, errs)
	}
}

// Ordinary and compiled searches must not claim that every placement fails
// when a pending withdrawal's required output was never recorded.
func TestPendingUnreplayablePlacementCannotProveFailure(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	bp, cp := filepath.Join(dir, "account.yaml"), filepath.Join(dir, "model.json")
	data, err := os.ReadFile("testdata/account.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), "  withdraw:\n    args: { amount: '{{.amount}}' }", "  withdraw:\n    args: { amount: '{{.obs.amount}}' }", 1)
	if err = os.WriteFile(bp, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile("testdata/account.qnt")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "account.qnt"), data, 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := Compile(bp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = cm.Write(cp); err != nil {
		t.Fatal(err)
	}
	ordinary, err := NewChecker(t.Context(), bp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer ordinary.Close()
	native, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	base := time.Unix(1, 0)
	steps := []qtrace.Step{
		{Recorder: "r", Seq: 1, Action: "deposit", Time: base, Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(10)}},
		{Recorder: "r", Seq: 2, Action: "withdraw", Time: base.Add(time.Second), Record: qtrace.Record{Invocation: true}, Args: map[string]any{"amount": int64(3)}},
		{Recorder: "r", Seq: 3, Action: "deposit", Time: base.Add(2 * time.Second), Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(12)}},
	}
	for _, c := range []*Checker{ordinary, native} {
		j, e := c.Judge(t.Context(), steps, SearchOptions{ResolvePending: true, ResolveBudget: 12})
		if j != nil || e == nil || errors.Is(e, ErrNeedsQuint) || !strings.Contains(e.Error(), "unreplayable placement") {
			t.Fatalf("compiled=%t: missing effect must not prove failure: %+v %v", c.compiled != nil, j, e)
		}
	}
}
