package validate

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func clockReceiptChecker(t *testing.T, file string) *Checker {
	t.Helper()
	skip.Quint(t)
	c, err := NewChecker(t.Context(), file, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func clockReceiptTime(ms int) time.Time {
	return time.Unix(100, 0).Add(time.Duration(ms) * time.Millisecond)
}

func clockReceiptHistory(readAt int) []qtrace.Step {
	return []qtrace.Step{
		{Action: "write", Recorder: "host-a", Actor: "registers", Process: "service", Seq: 1, Invoked: clockReceiptTime(2), Time: clockReceiptTime(3), Args: map[string]any{"key": "a", "value": int64(1)}},
		{Action: "read", Recorder: "host-b", Actor: "registers", Process: "service", Seq: 1, Invoked: clockReceiptTime(readAt), Time: clockReceiptTime(readAt + 1), Args: map[string]any{"key": "a"}, Obs: map[string]any{"value": int64(0)}},
	}
}

// These two observations have an explainable read-before-write order when
// the declared clock bound permits it. The missing read later than that bound
// remains a counterexample; recording the bound must not change either result.
func TestReceiptClockEvidenceRealJudgments(t *testing.T) {
	c := clockReceiptChecker(t, "../qtest/testdata/registers.yaml")
	for _, tc := range []struct {
		name   string
		readAt int
		opt    SearchOptions
		status receipt.Status
		want   receipt.ClockEvidence
	}{
		{"default conditional failure", 5, SearchOptions{Linearize: true}, receipt.Fail, receipt.ClockEvidence{Skew: "1ms", Source: "default", Constraints: 1, Recorders: []string{"host-a", "host-b"}, ClosestGap: "2ms", Conditional: true}},
		{"configured zero-edge pass", 5, SearchOptions{Linearize: true, Skew: 5 * time.Millisecond}, receipt.Pass, receipt.ClockEvidence{Skew: "5ms", Source: "configured"}},
		{"stale despite configured bound", 10, SearchOptions{Linearize: true, Skew: 5 * time.Millisecond}, receipt.Fail, receipt.ClockEvidence{Skew: "5ms", Source: "configured", Constraints: 1, Recorders: []string{"host-a", "host-b"}, ClosestGap: "7ms", Conditional: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j, err := c.Judge(t.Context(), clockReceiptHistory(tc.readAt), tc.opt)
			if err != nil {
				t.Fatal(err)
			}
			s := j.Report.Receipt()
			if len(s.Actors) != 1 || s.Actors[0].Status != tc.status || s.Actors[0].Search == nil || !reflect.DeepEqual(s.Actors[0].Search.Clock, &tc.want) {
				t.Fatalf("receipt clock evidence: %+v, want status %s clock %+v\n%s", s, tc.status, tc.want, j.Report.Summary())
			}
			// The same structured evidence reaches both whole-run and actor
			// views of the native report, even when the issue text is absent.
			doc := NewDocument("validate")
			doc.AddReport(j.Report)
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			var round Document
			if err := json.Unmarshal(raw, &round); err != nil {
				t.Fatal(err)
			}
			if round.Receipt == nil || !reflect.DeepEqual(round.Receipt.Actors[0].Search.Clock, &tc.want) || len(round.Actors) != 1 || !reflect.DeepEqual(round.Actors[0].Receipt.Search.Clock, &tc.want) {
				t.Fatalf("JSON lost clock evidence: %s", raw)
			}
			if !strings.Contains(s.String(), tc.want.Skew) || !strings.Contains(s.String(), tc.want.Source) {
				t.Fatalf("receipt rendering omits bound/source: %s", s.String())
			}
			if tc.status == receipt.Pass && (len(j.Steps) != 2 || j.Steps[0].Action != "read" || j.Steps[1].Action != "write") {
				t.Fatalf("passing witness is not the faithful order: %+v", j.Steps)
			}
			// Returned receipt edits cannot mutate the cached judgment.
			clock := s.Actors[0].Search.Clock
			clock.Skew = "999h"
			if len(clock.Recorders) > 0 {
				clock.Recorders[0] = "mutated"
			}
			if got := j.Report.Receipt().Actors[0].Search.Clock; !reflect.DeepEqual(got, &tc.want) {
				t.Fatalf("receipt clock aliases result storage: %+v", got)
			}
		})
	}
	for _, opt := range []SearchOptions{{}, {Skew: 5 * time.Millisecond}, {ResolvePending: true}} {
		j, err := c.Judge(t.Context(), clockReceiptHistory(5), opt)
		if err != nil {
			t.Fatal(err)
		}
		a := j.Report.Receipt().Actors[0]
		if a.Search != nil && a.Search.Clock != nil {
			t.Fatalf("non-linearized history claimed clock evidence: %+v", a.Search)
		}
	}
}

// Two imposed step-pair constraints can connect the same pair of recorders.
// The counter is neither a unique-recorder-pair count nor a minimal proof.
func TestReceiptClockCountsStepConstraints(t *testing.T) {
	c := clockReceiptChecker(t, "../qtest/testdata/registers.yaml")
	steps := []qtrace.Step{
		{Action: "write", Recorder: "a", Seq: 1, Invoked: clockReceiptTime(0), Time: clockReceiptTime(1), Args: map[string]any{"key": "a", "value": int64(1)}},
		{Action: "write", Recorder: "a", Seq: 2, Invoked: clockReceiptTime(1), Time: clockReceiptTime(2), Args: map[string]any{"key": "a", "value": int64(2)}},
		{Action: "read", Recorder: "b", Seq: 1, Invoked: clockReceiptTime(5), Time: clockReceiptTime(6), Args: map[string]any{"key": "a"}, Obs: map[string]any{"value": int64(2)}},
	}
	j, err := c.Judge(t.Context(), steps, SearchOptions{Linearize: true})
	if err != nil {
		t.Fatal(err)
	}
	clock := j.Report.Receipt().Actors[0].Search.Clock
	if !j.Report.OK() || clock.Constraints != 2 || clock.ClosestGap != "3ms" || !slices.Equal(clock.Recorders, []string{"a", "b"}) || clock.Conditional {
		t.Fatalf("two step constraints on one recorder pair: %+v\n%s", clock, j.Report.Summary())
	}
}

func TestReceiptClockBudgetIsNotConditionalFailure(t *testing.T) {
	c := clockReceiptChecker(t, "testdata/account.yaml")
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "anchor", Seq: 1, Invoked: clockReceiptTime(0), Time: clockReceiptTime(1), Args: map[string]any{"amount": int64(1)}, Obs: map[string]any{"balance": int64(1)}},
		{Action: "deposit", Recorder: "writer", Seq: 1, Invoked: clockReceiptTime(2), Time: clockReceiptTime(10), Args: map[string]any{"amount": int64(60)}, Obs: map[string]any{"balance": int64(61)}},
		{Action: "withdraw", Recorder: "reader", Seq: 1, Invoked: clockReceiptTime(3), Time: clockReceiptTime(5), Args: map[string]any{"amount": int64(50)}, Obs: map[string]any{"balance": int64(11)}},
	}
	j, err := c.Judge(t.Context(), steps, SearchOptions{Linearize: true, Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	a := j.Report.Receipt().Actors[0]
	if a.Status != receipt.Inconclusive || !a.Search.BudgetExhausted || a.Search.Clock == nil || a.Search.Clock.Constraints == 0 || a.Search.Clock.Conditional {
		t.Fatalf("budget exhaustion must not assert a clock-conditional counterexample: %+v\n%s", a, j.Report.Summary())
	}
	full, err := c.Judge(t.Context(), steps, SearchOptions{Linearize: true})
	if err != nil || !full.Report.OK() {
		t.Fatalf("budget control should have a conforming order: %v, %+v", err, full)
	}
}

func TestReceiptClockUnjudgedAndErrorsAreNotConditional(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *Result
		status receipt.Status
	}{
		{"tool error", &Result{Conforms: Fail, Failure: &Failure{Kind: "error"}}, receipt.Error},
		{"invariant evaluation error", &Result{Conforms: Pass, Invariants: []InvariantResult{{Name: "broken", Verdict: Fail, Failure: &Failure{Kind: "error"}}}}, receipt.Error},
		{"mixed counterexample and tool error", &Result{Conforms: Fail, Failure: &Failure{Kind: "disabled"}, Invariants: []InvariantResult{{Name: "broken", Verdict: Fail, Failure: &Failure{Kind: "error"}}}}, receipt.Fail},
		{"unjudged", &Result{Conforms: NotEvaluated}, receipt.Inconclusive},
		{"pass", &Result{Conforms: Pass}, receipt.Pass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.result.historySearch = &receipt.Search{Mode: "linearize", Clock: &receipt.ClockEvidence{Skew: "1ms", Source: "default", Constraints: 1, Recorders: []string{"a", "b"}, ClosestGap: "2ms", Conditional: true}}
			a := tc.result.Receipt()
			if a.Status != tc.status || a.Search.Clock.Conditional {
				t.Fatalf("non-counterexample claimed a conditional failure: %+v", a)
			}
		})
	}
}

func TestReceiptClockPartitionsKeepLocalConstraints(t *testing.T) {
	c := clockReceiptChecker(t, "../qtest/testdata/registers.yaml")
	steps := clockReceiptHistory(5)
	steps = append(steps, qtrace.Step{Action: "write", Recorder: "host-c", Actor: "registers", Process: "service", Seq: 1, Invoked: clockReceiptTime(20), Time: clockReceiptTime(21), Args: map[string]any{"key": "b", "value": int64(2)}})
	j, err := c.Judge(t.Context(), steps, SearchOptions{Linearize: true, Key: "key"})
	if err != nil {
		t.Fatal(err)
	}
	actors := j.Report.Receipt().Actors
	if len(actors) != 2 {
		t.Fatalf("want two partitions: %+v", actors)
	}
	for _, a := range actors {
		clock := a.Search.Clock
		switch a.Actor {
		case "registers key=a":
			if clock.Constraints != 1 || !slices.Equal(clock.Recorders, []string{"host-a", "host-b"}) || !clock.Conditional {
				t.Fatalf("a's evidence: %+v", a)
			}
		case "registers key=b":
			if clock.Constraints != 0 || len(clock.Recorders) != 0 || clock.Conditional || clock.Skew != "1ms" {
				t.Fatalf("b inherited another partition's clocks: %+v", a)
			}
		default:
			t.Fatalf("unexpected partition: %+v", a)
		}
	}
}

// Explicit domain order suppresses cross-recorder wall-clock constraints on
// reconstructed invocation/completion records, as the actual search does.
func TestReceiptClockDomainOrderSuppressesWallEdges(t *testing.T) {
	c := clockReceiptChecker(t, "../qtest/testdata/registers.yaml")
	steps := []qtrace.Step{
		{Action: "write", Recorder: "a", Seq: 1, Time: clockReceiptTime(2), Record: qtrace.Record{Invocation: true}, Args: map[string]any{"key": "a", "value": int64(1)}, Order: map[string]int64{"logical": 2}},
		{Action: "write", Recorder: "a", Seq: 2, Call: 1, Time: clockReceiptTime(3), Args: map[string]any{"key": "a", "value": int64(1)}, Order: map[string]int64{"logical": 2}},
		{Action: "read", Recorder: "b", Seq: 1, Time: clockReceiptTime(5), Record: qtrace.Record{Invocation: true}, Args: map[string]any{"key": "a"}, Order: map[string]int64{"logical": 1}},
		{Action: "read", Recorder: "b", Seq: 2, Call: 1, Time: clockReceiptTime(6), Args: map[string]any{"key": "a"}, Obs: map[string]any{"value": int64(0)}, Order: map[string]int64{"logical": 1}},
	}
	j, err := c.Judge(t.Context(), steps, SearchOptions{Linearize: true})
	if err != nil {
		t.Fatal(err)
	}
	clock := j.Report.Receipt().Actors[0].Search.Clock
	if !j.Report.OK() || clock.Constraints != 0 || len(clock.Recorders) != 0 || clock.ClosestGap != "" || clock.Skew != "1ms" {
		t.Fatalf("domain clock acquired fabricated wall-clock edges: %+v\n%s", clock, j.Report.Summary())
	}
}

// The completed-call edge count excludes pending calls; their possible effects
// belong to the separate resolution search rather than observed completions.
func TestReceiptClockCountsCompletedCallsOnly(t *testing.T) {
	c := clockReceiptChecker(t, "../qtest/testdata/registers.yaml")
	steps := []qtrace.Step{
		{Action: "write", Recorder: "a", Seq: 1, Invoked: clockReceiptTime(0), Time: clockReceiptTime(1), Args: map[string]any{"key": "a", "value": int64(1)}},
		{Action: "read", Recorder: "b", Seq: 1, Time: clockReceiptTime(5), Record: qtrace.Record{Invocation: true}, Args: map[string]any{"key": "a"}},
	}
	j, err := c.Judge(t.Context(), steps, SearchOptions{Linearize: true, ResolvePending: true})
	if err != nil {
		t.Fatal(err)
	}
	clock := j.Report.Receipt().Actors[0].Search.Clock
	if clock.Constraints != 0 || len(clock.Recorders) != 0 || clock.ClosestGap != "" || clock.Skew != "1ms" {
		t.Fatalf("pending call became completed-call clock evidence: %+v", clock)
	}
}
