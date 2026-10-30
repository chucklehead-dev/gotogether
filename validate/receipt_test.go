package validate

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func TestReceiptVerdictCategories(t *testing.T) {
	cases := []struct {
		name string
		r    *Result
		want receipt.Status
	}{
		{"pass", &Result{Conforms: Pass}, receipt.Pass},
		{"counterexample", &Result{Conforms: Fail, Failure: &Failure{Kind: "disabled"}}, receipt.Fail},
		{"budget", &Result{Conforms: NotEvaluated}, receipt.Inconclusive},
		{"invariant evaluation error", &Result{Conforms: Pass, Invariants: []InvariantResult{{Name: "broken", Verdict: Fail, Failure: &Failure{Kind: "error"}}}}, receipt.Error},
		{"evaluation error", &Result{Conforms: Fail, Failure: &Failure{Kind: "error"}}, receipt.Error},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Report{Actors: []*Result{tc.r}}
			s := r.Receipt()
			if s.Status != tc.want || s.Actors[0].Status != tc.want || s.OtherExecutions != receipt.NotChecked {
				t.Fatalf("%+v", s)
			}
			d := &Document{}
			d.AddReport(r)
			raw, _ := json.Marshal(d)
			if !strings.Contains(string(raw), `"receipt"`) || !strings.Contains(r.Summary(), s.String()) {
				t.Fatalf("receipt lost: %s", raw)
			}
		})
	}
	if s := (&Report{}).Receipt(); s.Status != receipt.NotChecked {
		t.Fatalf("empty report: %+v", s)
	}
}

// Real judgments must distinguish the order supplied from a search and carry
// the selected pending effect, without presenting it as observed behavior.
func TestReceiptHistorySearch(t *testing.T) {
	skip.Quint(t)
	c, err := NewChecker("testdata/blind.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	base := time.Unix(1, 0)
	steps := []qtrace.Step{
		{Recorder: "r", Seq: 1, Actor: "a", Thread: "t", Process: "p", Action: "deposit", Args: map[string]any{"amount": int64(10)}, Invoked: base, Time: base.Add(time.Second)},
	}
	for _, opt := range []SearchOptions{{}, {Linearize: true, Budget: 3}} {
		j, err := c.Judge(t.Context(), steps, opt)
		if err != nil {
			t.Fatal(err)
		}
		s := j.Report.Receipt()
		a := s.Actors[0]
		if a.Status != receipt.Pass {
			t.Fatalf("%+v", s)
		}
		if opt.Linearize {
			if a.Ordering != "searched" || a.Search == nil || a.Search.Goal != "one_conforming_history" || a.Search.OrderBudget != 3 {
				t.Fatalf("%+v", a)
			}
			a.Search.OrderBudget = 999
			if j.Report.Receipt().Actors[0].Search.OrderBudget != 3 {
				t.Fatal("receipt mutated cached result")
			}
		} else if a.Ordering != "reconstructed" || a.Search != nil {
			t.Fatalf("%+v", a)
		}
	}
	// Pending deposit must take effect for the observed withdrawal to conform.
	steps = []qtrace.Step{
		{Recorder: "r", Seq: 1, Actor: "a", Thread: "t", Process: "p", Action: "deposit", Args: map[string]any{"amount": int64(10)}, Time: base, Record: qtrace.Record{Invocation: true}},
		{Recorder: "r", Seq: 2, Actor: "a", Thread: "t", Process: "p", Action: "withdraw", Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}, Time: base.Add(time.Second)},
	}
	j, err := c.Judge(t.Context(), steps, SearchOptions{ResolvePending: true, ResolveBudget: 12})
	if err != nil {
		t.Fatal(err)
	}
	a := j.Report.Receipt().Actors[0]
	if a.Status != receipt.Pass || a.Search == nil || a.Search.Mode != "resolve_pending" || len(a.AssumedEffects) != 1 || a.AssumedEffects[0].Action != "deposit" || a.AssumedEffects[0].Args["amount"] != int64(10) {
		t.Fatalf("%+v\n%s", a, j.Report.Summary())
	}
}

func TestReceiptCompletionAndBudget(t *testing.T) {
	r := &Result{Actor: "a", Conforms: Pass, Absent: []Absence{{Kind: "eventually", Name: "eventually answered", Cause: Uncollected, Why: "no recorded settle"}}}
	a := r.Receipt()
	if len(a.Checks) != 2 || a.Checks[1].Status != receipt.NotChecked {
		t.Fatalf("%+v", a)
	}
	// Budget metadata must be supplied by the search, not inferred from prose.
	s := &search{searched: true, options: SearchOptions{Linearize: true, Budget: 1}, undecided: map[string]bool{"a": true}}
	rep := &Report{Actors: []*Result{{Actor: "a", Conforms: Fail}}}
	s.mark(rep)
	a = rep.Receipt().Actors[0]
	if a.Status != receipt.Inconclusive || !a.Search.BudgetExhausted {
		t.Fatalf("%+v", a)
	}
}

func TestReceiptKeepsFoldedPartitions(t *testing.T) {
	rep := &Report{Actors: []*Result{{Actor: "ledger acct=a", Partition: "acct=a", Conforms: Pass}, {Actor: "ledger acct=b", Partition: "acct=b", Conforms: Pass}}}
	d := &Document{}
	d.AddReport(rep)
	if len(d.Actors) != 0 || len(d.Partitions) != 1 || d.Receipt == nil || len(d.Receipt.Actors) != 2 {
		t.Fatalf("lost folded judgments: %+v", d)
	}
}

func TestReceiptCombinesReportsAndModelTrace(t *testing.T) {
	d := &Document{}
	d.AddReport(&Report{Actors: []*Result{{Actor: "recorded", Conforms: Pass}}})
	d.AddReport(&Report{Actors: []*Result{{Actor: "model", Conforms: Fail, inputScope: "model_trace", Failure: &Failure{Kind: "disabled"}}}})
	if len(d.Receipt.Actors) != 2 || d.Receipt.Scope != "mixed_inputs" || d.Receipt.Status != receipt.Fail || d.Actors[1].Receipt.Scope != "model_trace" {
		t.Fatalf("%+v", d.Receipt)
	}
}

func TestReceiptAssumedRecordJSON(t *testing.T) {
	step := &qtrace.Step{Recorder: "candidate", Seq: 1, Action: "deposit"}
	step.Ambiguous = true
	raw, err := json.Marshal(recordDoc(step, 0))
	if err != nil || !strings.Contains(string(raw), `"ambiguous":true`) {
		t.Fatalf("assumption hidden: %s %v", raw, err)
	}
	m, err := NewManifest(RunRecord{Results: []*Result{{Actor: "a", Conforms: Pass}}})
	if err != nil || m.Receipt == nil || m.Receipt.Status != receipt.Pass {
		t.Fatalf("manifest receipt: %+v %v", m, err)
	}
}
