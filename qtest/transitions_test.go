package qtest

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// These runs exercise the validator, rather than manufacturing a PASS bit. The
// narrow assertion must still check every captured transition after an invariant
// has already failed, and must never rewrite the ordinary report's verdict.
func TestTransitionsConformCheckedHistories(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	record := func(ops ...any) func(*qobs.Recorder) {
		return func(rec *qobs.Recorder) {
			for i := 0; i+2 < len(ops); i += 3 {
				rec.Record(context.Background(), ops[i].(string), nil, "amount", ops[i+1], "obs.balance", ops[i+2])
			}
		}
	}
	for _, c := range []struct {
		name        string
		ops         []any
		transitions bool
		ordinary    bool
	}{
		{"admitted", []any{"deposit", 5, 5}, true, true},
		{"disabled", []any{"deposit", 5, 5, "withdraw", 9, -4}, false, false},
		{"observed-state-mismatch", []any{"deposit", 5, 6}, false, false},
		{"invariant-failed", []any{"deposit", 110, 110}, true, false},
		{"illegal-after-invariant-failed", []any{"deposit", 110, 110, "withdraw", 200, -90}, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			run := Record(t, "../validate/testdata/account.yaml", record(c.ops...))
			before := transitionDocument(t, run.Report)
			verdict := run.Report.Verdict()
			if got := TransitionsConform().Holds(run); got != c.transitions {
				t.Fatalf("captured transitions=%v want=%v: %s", got, c.transitions, run.Report.Summary())
			}
			if got := Conforms().Holds(run); got != c.ordinary {
				t.Fatalf("ordinary conformance=%v want=%v", got, c.ordinary)
			}
			if !reflect.DeepEqual(verdict, run.Report.Verdict()) || string(before) != string(transitionDocument(t, run.Report)) {
				t.Fatal("scoped assertion rewrote native JSON or verdict")
			}
			if c.name == "invariant-failed" && run.Report.Verdict().Verdict != validate.Fail {
				t.Fatal("passing transition assertion hid invariant failure")
			}
		})
	}
	// Uncollected eventual properties are not checked by a transition assertion.
	unfinished := Record(t, "../validate/testdata/funds.binding.yaml", record("deposit", 5, 5, "withdraw", 5, 0))
	before := transitionDocument(t, unfinished.Report)
	if !TransitionsConform().Holds(unfinished) || Settles().Holds(unfinished) {
		t.Fatalf("unfinished prefix: %s", unfinished.Report.Summary())
	}
	if string(before) != string(transitionDocument(t, unfinished.Report)) {
		t.Fatal("unjudged eventual evidence changed")
	}
}

func transitionDocument(t *testing.T, report *validate.Report) []byte {
	t.Helper()
	d := validate.NewDocument("validate")
	d.AddReport(report)
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A genuinely active task prevents settlement; the admitted recorded transition
// remains independently useful evidence and does not make that system healthy.
func TestTransitionsConformPausedSystem(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	sys := System{Binding: "../validate/testdata/funds.binding.yaml", Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		s.Spawn("paused", func(ctx context.Context) {
			qobs.Record(ctx, "deposit", nil, "amount", 5, "obs.balance", 5)
			for {
				qenv.Sleep(ctx, time.Second)
			}
		})
		return s.Settle(3 * time.Second)
	}}
	runs := sys.Explore(t, ExploreConfig{Seeds: 2})
	runs.Expect(t, All(TransitionsConform()), None(Settles()))
	for _, run := range runs.Runs {
		if run.Report.Unsettled == "" || run.OK() || Conforms().Holds(run) {
			t.Fatalf("paused work became full conformance: %s", run.Report.Summary())
		}
	}
}

func transitionActor(name string, verdict validate.Verdict) *validate.Result {
	return &validate.Result{Actor: name, Steps: 1, Conforms: verdict}
}

func TestTransitionsConformThreeValued(t *testing.T) {
	t.Parallel()
	pass := func() *validate.Report {
		return &validate.Report{Actors: []*validate.Result{transitionActor("a", validate.Pass)}}
	}
	illegal := func() *validate.Report {
		r := pass()
		r.Actors[0].Conforms = validate.Fail
		r.Actors[0].Failure = &validate.Failure{Kind: "disabled", Step: &qtrace.Step{Action: "withdraw"}}
		return r
	}
	toolError := func() *validate.Report {
		r := pass()
		r.Actors[0].Conforms = validate.Fail
		r.Actors[0].Failure = &validate.Failure{Kind: "error"}
		return r
	}
	for _, c := range []struct {
		name   string
		report *validate.Report
		want   tri
	}{
		{"pass", pass(), yes},
		{"illegal", illegal(), no},
		{"nil-report", nil, unknown},
		{"no-actors", &validate.Report{}, unknown},
		{"nil-actor", &validate.Report{Actors: []*validate.Result{nil}}, unknown},
		{"no-transitions", &validate.Report{Actors: []*validate.Result{{Actor: "a", Conforms: validate.Pass}}}, unknown},
		{"not-judged", &validate.Report{Actors: []*validate.Result{transitionActor("a", validate.NotEvaluated)}}, unknown},
		{"tool-error", toolError(), unknown},
		{"explicit-error-with-pass-label", &validate.Report{Actors: []*validate.Result{{Actor: "a", Steps: 1, Conforms: validate.Pass, Failure: &validate.Failure{Kind: "error"}}}}, unknown},
		{"search-exhausted", &validate.Report{Actors: []*validate.Result{{Actor: "a", Steps: 1, Conforms: validate.NotEvaluated, Choices: &validate.ChoiceSearch{Status: "exhausted", Limit: 1, Explored: 1}}}}, unknown},
		{"unsupported-choices", &validate.Report{Actors: []*validate.Result{{Actor: "a", Steps: 1, Conforms: validate.NotEvaluated, Choices: &validate.ChoiceSearch{Status: "unsupported"}}}}, unknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := TransitionsConform()
			if got := e.value(c.report); got != c.want {
				t.Fatalf("value=%v want=%v", got, c.want)
			}
			if got := e.Holds(&Run{Report: c.report}); got != (c.want == yes) {
				t.Fatalf("Holds=%v want=%v", got, c.want == yes)
			}
			wantNot := unknown
			if c.want == yes {
				wantNot = no
			} else if c.want == no {
				wantNot = yes
			}
			if got := Not(e).value(c.report); got != wantNot {
				t.Fatalf("negation=%v want=%v", got, wantNot)
			}
			for _, q := range []struct {
				claim Claim
				want  bool
			}{{All(e), c.want == yes}, {Some(e), c.want == yes}, {None(e), c.want == no}} {
				j := q.claim.judge([]*validate.Report{c.report})
				if j.ok != q.want {
					t.Fatalf("%s ok=%v want=%v: %+v", q.claim, j.ok, q.want, j)
				}
			}
		})
	}
	// A decided counterexample is still evidence when a different actor has no
	// answer; uncertainty may not turn it into a passing universal assertion.
	mixed := &validate.Report{Actors: []*validate.Result{illegal().Actors[0], transitionActor("b", validate.NotEvaluated)}}
	if TransitionsConform().value(mixed) != no || !None(TransitionsConform()).judge([]*validate.Report{mixed}).ok {
		t.Fatal("another actor's missing verdict hid a captured illegal transition")
	}
	for _, claim := range []Claim{All(TransitionsConform()), Some(TransitionsConform()), None(TransitionsConform())} {
		if claim.judge(nil).ok {
			t.Fatalf("%s held over no runs", claim)
		}
	}
}

func TestTransitionsConformActorScope(t *testing.T) {
	t.Parallel()
	bad := transitionActor("other", validate.Fail)
	bad.Failure = &validate.Failure{Kind: "disabled"}
	report := &validate.Report{Actors: []*validate.Result{transitionActor("wanted", validate.Pass), bad}}
	if TransitionsConform().value(report) != no || TransitionsConform("wanted").value(report) != yes {
		t.Fatal("exact actor selection broadened or ignored its scope")
	}
	for _, names := range [][]string{{"missing"}, {"wanted", "missing"}, {"want*"}} {
		e := TransitionsConform(names...)
		if e.value(report) != unknown || All(e).judge([]*validate.Report{report}).ok || None(e).judge([]*validate.Report{report}).ok {
			t.Fatalf("missing exact actor scope %v passed a positive or negative claim", names)
		}
	}
	names := []string{"wanted", "wanted"}
	e := TransitionsConform(names...)
	names[0], names[1] = "other", "other"
	if e.value(report) != yes {
		t.Fatal("caller mutation changed captured actor selection")
	}
	// Empty is the native name of the default actor, not a wildcard or an
	// omitted selector. Reports render it for humans but keep the empty value.
	report.Actors = append(report.Actors, transitionActor("", validate.Pass))
	if TransitionsConform("").value(report) != yes || TransitionsConform().value(report) != no {
		t.Fatal("default actor selection broadened to other actors")
	}
	// None must use this expectation's own scope and tri-state policy without
	// inspecting an unrelated nil actor through the broad undecided helper.
	report.Actors = append(report.Actors, nil)
	if TransitionsConform("other").value(report) != no || !None(TransitionsConform("other")).judge([]*validate.Report{report}).ok || !None(TransitionsConform()).judge([]*validate.Report{report}).ok {
		t.Fatal("unrelated missing evidence hid a clean rejection or panicked quantification")
	}
}

func TestTransitionsConformDoesNotJudgeOtherChecks(t *testing.T) {
	t.Parallel()
	for _, status := range []receipt.Status{receipt.Pass, receipt.Fail, receipt.Inconclusive, receipt.NotChecked, receipt.Error} {
		t.Run(string(status), func(t *testing.T) {
			report := &validate.Report{Unsettled: "paused", Actors: []*validate.Result{transitionActor("a", validate.Pass)}, Composition: &receipt.Composition{Local: []receipt.Component{{Target: "peer", Applicable: true, Status: status}}}}
			report.Actors[0].Invariants = []validate.InvariantResult{{Name: "safe", Verdict: validate.Fail, Failure: &validate.Failure{Kind: "invariant"}}}
			before := transitionDocument(t, report)
			if TransitionsConform().value(report) != yes || Conforms().Holds(&Run{Report: report}) || report.OK() {
				t.Fatal("transition assertion adopted or cleared a different check's verdict")
			}
			if string(before) != string(transitionDocument(t, report)) {
				t.Fatal("transition assertion changed native report")
			}
		})
	}
}

// Loss/collection markers disclose what is outside the capture. A native PASS
// still establishes the checked prefix; an actual native downgrade does not.
// Reconstruction errors, by contrast, make even that captured order unreliable.
func TestTransitionsConformEvidenceScope(t *testing.T) {
	t.Parallel()
	for _, metadata := range []string{"lost-suffix", "lost-prefix", "gap", "collected", "pending", "warning"} {
		t.Run(metadata, func(t *testing.T) {
			actor := transitionActor("a", validate.Pass)
			switch metadata {
			case "lost-suffix":
				actor.Lost = []qtrace.Lost{{Recorder: "r", After: 1}}
			case "lost-prefix":
				actor.Lost = []qtrace.Lost{{Recorder: "r", Prefix: true, After: 1}}
			case "gap":
				actor.Gaps = []qtrace.Gap{{Recorder: "r", First: 2, Last: 2}}
			case "collected":
				actor.Collected = []qtrace.Watermark{{Recorder: "r", Through: time.Unix(1, 0)}}
			case "pending":
				actor.Pending = []qtrace.Step{{Action: "withdraw", Record: qtrace.Record{Invocation: true}}}
			case "warning":
				actor.Issues = []qtrace.Issue{{Severity: "warn", Kind: "unordered", Msg: "observed ordering selected"}}
			}
			report := &validate.Report{Actors: []*validate.Result{actor}}
			if TransitionsConform().value(report) != yes {
				t.Fatal("capture boundary erased native checked-prefix conformance")
			}
			actor.Conforms = validate.NotEvaluated
			if TransitionsConform().value(report) != unknown || None(TransitionsConform()).judge([]*validate.Report{report}).ok {
				t.Fatal("native no-verdict became evidence of nonconformance")
			}
		})
	}
	cleanFail := transitionActor("bad", validate.Fail)
	cleanFail.Failure = &validate.Failure{Kind: "disabled"}
	tainted := transitionActor("tainted", validate.Fail)
	tainted.Failure = &validate.Failure{Kind: "disabled"}
	tainted.Issues = []qtrace.Issue{{Severity: "error", Kind: "cycle", Msg: "captured ordering is contradictory"}}
	report := &validate.Report{Actors: []*validate.Result{tainted, transitionActor("good", validate.Pass), cleanFail}}
	if TransitionsConform("tainted").value(report) != unknown || None(TransitionsConform("tainted")).judge([]*validate.Report{report}).ok {
		t.Fatal("reconstruction error manufactured a counterexample")
	}
	if TransitionsConform("good").value(report) != yes {
		t.Fatal("another actor's reconstruction error contaminated an explicitly selected clean actor")
	}
	if TransitionsConform().value(report) != no || TransitionsConform("bad", "missing").value(report) != no {
		t.Fatal("clean rejection lost to an unrelated undecided actor")
	}
	report.Issues = []qtrace.Issue{{Severity: "error", Kind: "cycle", Msg: "global captured ordering is contradictory"}}
	if TransitionsConform().value(report) != unknown || TransitionsConform("good").value(report) != unknown || TransitionsConform("bad").value(report) != unknown {
		t.Fatal("unscoped reconstruction error was ignored")
	}
}

func TestTransitionsConformRejectsContradictoryCapture(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	// Program order requires seq 1 before seq 2, while the domain clock
	// requires the opposite. A fallback replay may have a model answer, but
	// there is no consistent captured history on which to base this claim.
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "r", Actor: "account", Thread: "worker", Seq: 1, Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}, Order: map[string]int64{"position": 2}},
		{Action: "deposit", Recorder: "r", Actor: "account", Thread: "worker", Seq: 2, Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(10)}, Order: map[string]int64{"position": 1}},
	}
	run := Validate(t, "../validate/testdata/account.yaml", steps)
	seen := false
	for _, issue := range run.Report.Issues {
		seen = seen || issue.Severity == "error" && issue.Kind == "cycle"
	}
	for _, actor := range run.Report.Actors {
		for _, issue := range actor.Issues {
			seen = seen || issue.Severity == "error" && issue.Kind == "cycle"
		}
	}
	if !seen {
		t.Fatal("fixture did not produce contradictory captured ordering")
	}
	if TransitionsConform().value(run.Report) != unknown || None(TransitionsConform()).judge([]*validate.Report{run.Report}).ok {
		t.Fatal("contradictory capture produced a conclusive positive or negative assertion")
	}
}
