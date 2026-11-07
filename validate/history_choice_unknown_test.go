package validate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func historyChoiceChecker(t *testing.T) *Checker {
	t.Helper()
	needQuint(t)
	dir := t.TempDir()
	const model = `module overlap {
  var x: int
  action init = x' = 0
  action choose = { nondet value = Set(1, 2).oneOf(); x' = value }
  action double = x' = 2 * x
  action writeValue(value: int): bool = x' = value
  action read = x' = x
  action step = any { choose, double, read }
  val safe = x >= 0
}`
	const binding = "spec: model.qnt\nmodule: overlap\nactions:\n  choose: {}\n  double: {}\n  writeValue: {}\n  read:\n    expect: 'x == {{.obs.value}}'\ninvariants: [safe]\n"
	for name, body := range map[string]string{"model.qnt": model, "binding.yaml": binding} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := NewChecker(t.Context(), filepath.Join(dir, "binding.yaml"), Options{Eval: "go", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func historyChoiceCalls(actor string, witness bool) []qtrace.Step {
	at := func(n int) time.Time { return time.Unix(100, 0).Add(time.Duration(n) * 10 * time.Millisecond) }
	h := []qtrace.Step{
		{Actor: actor, Action: "choose", Recorder: actor + "a", Seq: 1, Thread: "a", Invoked: at(0), Time: at(10)},
		{Actor: actor, Action: "double", Recorder: actor + "b", Seq: 1, Thread: "b", Invoked: at(0), Time: at(10)},
		{Actor: actor, Action: "read", Recorder: actor + "c", Seq: 1, Thread: "c", Invoked: at(20), Time: at(30), Obs: map[string]any{"value": int64(1)}},
	}
	if witness {
		h[0], h[1] = h[1], h[0]
		// Prefer this permitted order in the reconstructed canonical candidate,
		// while keeping the two writes concurrent.
		h[1].Time = at(11)
	}
	return h
}

func TestHistoryChoiceUnknownCannotRefuteAllowedWitness(t *testing.T) {
	c := historyChoiceChecker(t)
	history := historyChoiceCalls("unknown", false)
	witness := historyChoiceCalls("witness", true)
	for i := range witness {
		witness[i].Recorder, witness[i].Thread, witness[i].Seq = "witness", "witness", uint64(i+1)
	}
	positive, err := c.Check(t.Context(), witness)
	if err != nil || !positive.OK() || positive.Actors[0].Choices.Confirmation != "path_confirmed" {
		t.Fatalf("allowed original-action witness did not pass pinned Quint confirmation: %v, %v", positive, err)
	}
	j, err := c.Judge(t.Context(), history, SearchOptions{Linearize: true, Budget: 200})
	if err != nil {
		t.Fatal(err)
	}
	a := j.Report.Actors[0]
	if a.Conforms != NotEvaluated || a.Failure != nil || a.Choices == nil || a.Choices.Status != "unsupported" || !strings.Contains(a.noVerdict, "without history search") {
		t.Fatalf("unsupported history became a model refutation: %s", j.Report.Summary())
	}
	r := a.Receipt()
	if r.Status != receipt.Inconclusive || r.Search == nil || r.Search.BudgetExhausted || r.Search.Clock.Conditional {
		t.Fatalf("unsupported candidate became a budget/clock absence proof: %+v", r)
	}
	for _, inv := range a.Invariants {
		if inv.Verdict != NotEvaluated || inv.Failure != nil {
			t.Fatalf("unsupported actor acquired a property verdict: %+v", inv)
		}
	}
	for _, issue := range j.Report.Issues {
		if strings.Contains(issue.Msg, "no order of") {
			t.Fatalf("unsupported candidate produced an absence claim: %+v", issue)
		}
	}
}

func TestHistoryChoiceUnknownStopsBeforePrefixProof(t *testing.T) {
	c := historyChoiceChecker(t)
	traces, _ := qtrace.Reconstruct(historyChoiceCalls("unknown", false))
	tr := traces[0]
	callback := quintChecker(inHistory(c, tr), tr, tr.Actor, false)
	returned, caught := false, false
	func() {
		defer func() {
			p := recover()
			u, ok := p.(unjudgedCandidate)
			if !ok {
				t.Fatalf("want explicit candidate no-verdict, got %v", p)
			}
			caught = u.result.Conforms == NotEvaluated && u.result.Choices.Status == "unsupported"
		}()
		_, _ = qtrace.Linearize(tr.Steps, qtrace.LinearizeOptions{Budget: 200}, func(orders [][]qtrace.Step) ([]int, []string, error) {
			fs, keys := callback(orders)
			return fs, keys, nil
		})
		returned = true
	}()
	if !caught || returned {
		t.Fatal("unsupported candidate entered a prefix cache or returned an absence proof")
	}
}

func TestHistoryChoiceUnknownIsScopedAndPositiveWitnessStands(t *testing.T) {
	c := historyChoiceChecker(t)
	history := append(historyChoiceCalls("unknown", false), historyChoiceCalls("positive", true)...)
	negative := historyChoiceCalls("negative", false)
	negative = []qtrace.Step{negative[0], negative[2]}
	negative[0].Action, negative[0].Args = "writeValue", map[string]any{"value": int64(2)}
	history = append(history, negative...)
	j, err := c.Judge(t.Context(), history, SearchOptions{Linearize: true, Budget: 200})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]Verdict{}
	for _, a := range j.Report.Actors {
		seen[a.Actor] = a.Conforms
		if a.Actor == "positive" && (a.Choices == nil || a.Choices.Confirmation != "path_confirmed") {
			t.Fatal("positive history was not confirmed by its actual pinned witness")
		}
	}
	if seen["unknown"] != NotEvaluated || seen["positive"] != Pass || seen["negative"] != Fail {
		t.Fatalf("one actor's unsupported search changed others: %v\n%s", seen, j.Report.Summary())
	}
}

func TestHistoryChoiceOperationalErrorRemainsError(t *testing.T) {
	c := historyChoiceChecker(t)
	c.Options.Eval, c.Options.Quint = "quint", filepath.Join(t.TempDir(), "missing-quint")
	h := historyChoiceCalls("tool", false)
	h = []qtrace.Step{h[0], h[2]}
	h[0].Action, h[0].Args = "writeValue", map[string]any{"value": int64(1)}
	j, err := c.Judge(t.Context(), h, SearchOptions{Linearize: true, Budget: 200})
	if err == nil || j != nil {
		t.Fatalf("tool error became a model no-verdict: %v, %v", j, err)
	}
}

func TestHistoryChoicePendingResolutionRemainsUnknown(t *testing.T) {
	c := historyChoiceChecker(t)
	h := historyChoiceCalls("unknown", false)
	h = append(h, qtrace.Step{Actor: "unknown", Action: "choose", Recorder: "pending", Seq: 1,
		Record: qtrace.Record{Invocation: true}, Time: time.Unix(100, 0)})
	for _, linearize := range []bool{false, true} {
		j, err := c.Judge(t.Context(), h, SearchOptions{Linearize: linearize, ResolvePending: true, Budget: 200, ResolveBudget: 200})
		if err != nil {
			t.Fatal(err)
		}
		a := j.Report.Actors[0]
		if a.Conforms != NotEvaluated || a.Failure != nil || a.Choices == nil || a.Choices.Status != "unsupported" || a.Receipt().Search.BudgetExhausted {
			t.Fatalf("unsupported pending resolution became a proof/cut (linearize %v): %s", linearize, j.Report.Summary())
		}
		for _, issue := range j.Report.Issues {
			if strings.Contains(issue.Msg, "no resolution") || strings.Contains(issue.Msg, "no behaviour of the model") || strings.Contains(issue.Msg, "no order of") {
				t.Fatalf("unavailable pending candidate manufactured an absence issue: %+v", issue)
			}
		}
	}
}

func TestChoiceWitnessIsNotAHistoryStateKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "witness.itf.json")
	if err := os.WriteFile(path, []byte(`{"states":[{"x":1}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	r := &Report{Actors: []*Result{{Conforms: Pass, ITF: path}}}
	if lastState(r) == "" {
		t.Fatal("deterministic ITF lost its state key")
	}
	r.Actors[0].Choices = &ChoiceSearch{Status: "matched", Confirmation: "path_confirmed"}
	if lastState(r) != "" {
		t.Fatal("one pinned model-choice witness became a complete future-state key")
	}
}

func TestHistoryUnknownPreservesPositiveConformanceAndProperties(t *testing.T) {
	confirmed := &ChoiceSearch{Status: "matched", Confirmation: "path_confirmed"}
	a := &Result{Actor: "known", Conforms: Pass, Choices: confirmed, byEvaluator: true, sampled: true,
		Invariants: []InvariantResult{
			{Name: "held", Verdict: Pass},
			{Name: "failed-on-one-order", Verdict: Fail, Failure: &Failure{Kind: "state"}},
			{Name: "not-checked", Verdict: NotEvaluated},
		}}
	unknown := &Result{Conforms: NotEvaluated, noVerdict: "unsupported history candidate", Choices: &ChoiceSearch{Status: "unsupported"}}
	rep := &Report{Actors: []*Result{a}}
	s := &search{unjudged: map[string]*Result{"known": unknown}}
	s.mark(rep)
	if a.Conforms != Pass || a.Choices != confirmed || !a.byEvaluator || !a.sampled || a.Invariants[0].Verdict != Pass {
		t.Fatalf("unsupported enumeration erased independently checked positive facts: %+v", a)
	}
	if a.Invariants[1].Verdict != NotEvaluated || a.Invariants[1].Failure != nil || a.Invariants[2].Verdict != NotEvaluated || a.noVerdict != unknown.noVerdict {
		t.Fatalf("one candidate's properties became a history absence proof: %+v", a)
	}
}

func TestHistoryUnknownDoesNotReplaceOperationalFailure(t *testing.T) {
	failure := &Failure{Kind: "error", Quint: "backend failed"}
	a := &Result{Conforms: Fail, Failure: failure}
	markUnjudgedHistory(a, &Result{Conforms: NotEvaluated, noVerdict: "unsupported candidate"})
	if a.Failure != failure || a.Conforms != Fail || a.noVerdict != "" || a.Receipt().Status != receipt.Error {
		t.Fatalf("operational failure became unsupported model evidence: %+v", a)
	}
}

func TestHistoryChoiceDeferredConfirmationKeepsUnknown(t *testing.T) {
	c := historyChoiceChecker(t)
	h := historyChoiceCalls("unknown", false)
	traces, _ := qtrace.Reconstruct(h)
	tr := traces[0]
	rep, err := c.Check(t.Context(), h)
	if err != nil {
		t.Fatal(err)
	}
	s := &search{claims: map[string]*claim{"unknown": {ch: inHistory(c, tr), tr: tr, order: h}}}
	if again, err := s.confirm(rep); err != nil || again != nil {
		t.Fatalf("unavailable confirmation became an operational error/rejection: %v, %v", again, err)
	}
	s.mark(rep)
	if rep.Actors[0].Conforms != NotEvaluated || rep.Actors[0].Choices.Status != "unsupported" {
		t.Fatalf("unavailable confirmation became a model refutation: %s", rep.Summary())
	}
}

func historyPropertyPhaseChecker(t *testing.T) *Checker {
	t.Helper()
	needQuint(t)
	dir := t.TempDir()
	const model = `module propertyPhase {
  var x: int
  action init = x' = 0
  action write = { nondet ignored = Set(1, 2).oneOf(); x' = 1 }
  action read = x' = x
  action step = any { write, read }
  val safe = x == 0
}`
	const binding = "spec: model.qnt\nmodule: propertyPhase\nactions:\n  write: {}\n  read:\n    expect: 'x == {{.obs.value}}'\ninvariants: [safe]\n"
	for name, body := range map[string]string{"model.qnt": model, "binding.yaml": binding} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := NewChecker(t.Context(), filepath.Join(dir, "binding.yaml"), Options{Eval: "go", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestHistoryChoiceInvariantUnknownRetainsReorderedConformance(t *testing.T) {
	c := historyPropertyPhaseChecker(t)
	at := func(n int) time.Time { return time.Unix(100, 0).Add(time.Duration(n) * 10 * time.Millisecond) }
	h := []qtrace.Step{
		{Action: "read", Recorder: "r", Thread: "r", Seq: 1, Invoked: at(0), Time: at(5), Obs: map[string]any{"value": int64(1)}},
		{Action: "write", Recorder: "w", Thread: "w", Seq: 1, Invoked: at(0), Time: at(10)},
	}
	bad, err := c.Check(t.Context(), h)
	if err != nil || bad.Actors[0].Conforms != Fail {
		t.Fatalf("control: recorded order must fail: %v, %v", bad, err)
	}
	j, err := c.Judge(t.Context(), h, SearchOptions{Linearize: true, Budget: 200})
	if err != nil {
		t.Fatal(err)
	}
	a := j.Report.Actors[0]
	if a.Conforms != Pass || len(j.Steps) != 2 || j.Steps[0].Action != "write" || a.Choices == nil || a.Choices.Confirmation != "path_confirmed" {
		t.Fatalf("invariant uncertainty erased the reordered positive witness: %s", j.Report.Summary())
	}
	if a.Invariants[0].Verdict != NotEvaluated || a.Invariants[0].Failure != nil || !strings.Contains(a.noVerdict, "without history search") || a.Receipt().Search.BudgetExhausted {
		t.Fatalf("unsupported invariant search manufactured a property proof: %s", j.Report.Summary())
	}
}

func TestHistoryUnknownKeepsPropertyOperationalErrors(t *testing.T) {
	for _, conformance := range []Verdict{Pass, Fail} {
		t.Run(string(conformance), func(t *testing.T) {
			failure := &Failure{Kind: "error", Quint: "property backend unavailable"}
			a := &Result{Conforms: conformance, Invariants: []InvariantResult{{Name: "safe", Verdict: Fail, Failure: failure}}}
			if conformance == Fail {
				a.Failure = &Failure{Kind: "state"}
			}
			markUnjudgedHistory(a, &Result{Conforms: NotEvaluated, noVerdict: "unsupported history candidate"})
			if a.Invariants[0].Failure != failure || a.Invariants[0].Verdict != Fail || a.Receipt().Status != receipt.Error {
				t.Fatalf("property operational error became model uncertainty: %+v", a)
			}
		})
	}
}

func TestHistoryChoiceInvariantUnknownRetainsPendingEffect(t *testing.T) {
	c := historyPropertyPhaseChecker(t)
	at := time.Unix(100, 0)
	h := []qtrace.Step{
		{Action: "read", Recorder: "reader", Thread: "reader", Seq: 1, Invoked: at, Time: at.Add(100 * time.Millisecond), Obs: map[string]any{"value": int64(1)}},
		{Action: "write", Recorder: "writer", Seq: 1, Record: qtrace.Record{Invocation: true}, Time: at},
	}
	bad, err := c.Check(t.Context(), h)
	if err != nil || bad.Actors[0].Conforms != Fail {
		t.Fatalf("control: read without the pending effect must fail: %v, %v", bad, err)
	}
	j, err := c.Judge(t.Context(), h, SearchOptions{ResolvePending: true, ResolveBudget: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Steps) < 2 || j.Steps[0].Action != "write" || !j.Steps[0].Ambiguous || j.Steps[1].Action != "read" {
		t.Fatalf("invariant uncertainty discarded the found pending placement: %+v", j.Steps)
	}
	a := j.Report.Actors[0]
	if a.Conforms != NotEvaluated || a.Failure != nil || a.Choices == nil || a.Choices.Status != "unsupported" || !strings.Contains(a.noVerdict, "without assumed effects") || a.Receipt().Search.BudgetExhausted {
		t.Fatalf("unsupported assumed-effect confirmation became a proof: %s", j.Report.Summary())
	}
	for _, issue := range j.Report.Issues {
		if strings.Contains(issue.Msg, "no resolution") || strings.Contains(issue.Msg, "no behaviour of the model") {
			t.Fatalf("invariant uncertainty manufactured pending-call absence: %+v", issue)
		}
	}
}

func TestCandidateFailureSeparatesUnknownFromRejection(t *testing.T) {
	unknown := &Result{Conforms: NotEvaluated, noVerdict: "unsupported"}
	_, got := candidateFailure(&Report{Actors: []*Result{unknown}}, 1)
	if got != unknown {
		t.Fatal("unavailable candidate was treated as rejection")
	}
	if at, u := candidateFailure(&Report{Actors: []*Result{{Conforms: Pass}}}, 1); at != -1 || u != nil {
		t.Fatal("positive candidate lost")
	}
	if at, u := candidateFailure(&Report{Actors: []*Result{{Conforms: Fail}}}, 1); at != 0 || u != nil {
		t.Fatal("known rejection lost")
	}
}

func TestHistoryChoiceUnknownDoesNotHideBatchOperationalError(t *testing.T) {
	c := historyChoiceChecker(t)
	real, err := exec.LookPath("quint")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HISTORY_REAL_QUINT", real)
	tool := filepath.Join(t.TempDir(), "quint")
	script := `#!/bin/sh
if [ "$1" = test ]; then echo deliberate-history-driver-failure >&2; exit 42; fi
exec "$HISTORY_REAL_QUINT" "$@"
`
	if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	c.Options.Eval, c.Options.Quint = "quint", tool
	traces, _ := qtrace.Reconstruct(historyChoiceCalls("batch", false))
	tr := traces[0]
	first := []qtrace.Step{tr.Steps[0]}
	second := append([]qtrace.Step(nil), first...)
	second[0].Action, second[0].Args = "writeValue", map[string]any{"value": int64(1)}
	defer func() {
		p := recover()
		e, ok := p.(searchError)
		if !ok || !strings.Contains(e.Error(), "deliberate-history-driver-failure") {
			t.Fatalf("unavailable first candidate concealed operational error: %v", p)
		}
	}()
	quintChecker(inHistory(c, tr), tr, tr.Actor, false)([][]qtrace.Step{first, second})
	t.Fatal("operational failure did not stop search")
}

func TestHistoryUnknownAlternativeKeepsWitnessWithoutPropertyRefutation(t *testing.T) {
	c := historyPropertyPhaseChecker(t)
	h := []qtrace.Step{{Action: "write", Seq: 1}}
	report, err := c.Check(t.Context(), h)
	if err != nil || report.Actors[0].Conforms != Pass || report.Actors[0].Invariants[0].Verdict != Fail {
		t.Fatalf("positive witness with property violation control: %v, %v", report, err)
	}
	unknown := &Result{Conforms: NotEvaluated, noVerdict: "earlier placement unsupported"}
	out, issue, cut, cl, u := checkedHistory(&qtrace.Trace{}, func() ([]qtrace.Step, qtrace.Issue, bool, *claim) {
		// A completed resolution batch contains an unknown earlier placement
		// and this independently checkable later conformance witness.
		panic(unjudgedCandidate{result: unknown, witness: h})
	})
	if len(out) != 1 || cut || cl != nil || u != unknown || strings.Contains(issue.Msg, "no resolution") {
		t.Fatalf("unknown alternative became an absence proof: %+v, %+v", out, issue)
	}
	markUnjudgedHistory(report.Actors[0], u)
	if report.Actors[0].Conforms != Pass || report.Actors[0].Invariants[0].Verdict != NotEvaluated {
		t.Fatalf("known witness or unknown alternative lost: %s", report.Summary())
	}
}
