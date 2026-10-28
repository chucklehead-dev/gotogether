package validate

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// oncallRun is a payments day as a step log read it: each step's
// record day.jsonl:<line> (line 1 the format record), at its wall time,
// by process payments-1. ops are "action id amount outcome".
func oncallRun(ops ...string) []qtrace.Step {
	t0 := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	var out []qtrace.Step
	for i, op := range ops {
		f := strings.Fields(op)
		s := qtrace.Step{Action: f[0], Seq: uint64(i + 1), Recorder: "payments-1", Process: "payments-1", Thread: "payments-1", Actor: "payments",
			Outcome: f[len(f)-1], Time: t0.Add(time.Duration(i) * time.Second), Args: map[string]any{"id": f[1]},
			Source: fmt.Sprintf("day.jsonl:%d", i+2)}
		if len(f) == 4 {
			var n int64
			fmt.Sscan(f[2], &n)
			s.Args["amount"] = n
		}
		out = append(out, s)
	}
	return out
}

// The on-call report (I20) names a failing record once, by where it was
// read, with its wall time and process; says what the step changed in
// the model's state, by path in the model's names, not a dump cut at
// 240 characters; and leaves the replay's step and the recorder's seq to
// -v. A refund past its capture violates refundsWithinCapture.
func TestOnCallViolationReport(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/oncall.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := c.Check(context.Background(), oncallRun("authorize ch_1 ok", "capture ch_1 1000 ok", "refund ch_1 700 ok", "refund ch_1 500 ok"))
	if err != nil {
		t.Fatal(err)
	}
	sum := rep.Summary()
	t.Logf("%s", sum)
	for _, want := range []string{
		"violated after record day.jsonl:5\n",
		"record:   day.jsonl:5, 2026-10-02T09:00:03Z, process payments-1\n",
		"observed: refund(amount=500, id=ch_1)\n",
		"the step changed, in the model's state:\n",
		`charges["ch_1"].refunded: 700 -> 1200`,
		"model state after the step: 1 variable (-v prints it)",
	} {
		if !strings.Contains(sum, want) {
			t.Errorf("the report lacks %q:\n%s", want, sum)
		}
	}
	for _, not := range []string{"step 4", "seq 4", "charges = Map("} {
		if strings.Contains(sum, not) {
			t.Errorf("the report names %q, which is -v's:\n%s", not, sum)
		}
	}
	v := rep.VerboseSummary()
	for _, want := range []string{"step 4 of the replay, seq 4 of recorder payments-1", "charges = Map("} {
		if !strings.Contains(v, want) {
			t.Errorf("the verbose report lacks %q:\n%s", want, v)
		}
	}
	d := NewDocument("validate")
	d.AddReport(rep)
	if len(d.Actors) != 1 || len(d.Actors[0].Failures) != 1 {
		t.Fatalf("the document: %s", d.JSON())
	}
	f := d.Actors[0].Failures[0]
	if f.Check != "refundsWithinCapture" || f.Verdict != "fail" || f.Record == nil || f.Record.ID != "day.jsonl:5" || f.Record.Step != 4 || f.Record.Seq != 4 ||
		f.Record.Time != "2026-10-02T09:00:03Z" || f.Record.Process != "payments-1" || len(f.Changes) != 1 ||
		f.Changes[0] != (Change{Path: `charges["ch_1"].refunded`, Kind: "changed", Before: "700", After: "1200"}) ||
		f.Brief != `refund(amount=500, id=ch_1): charges["ch_1"].refunded: 700 -> 1200` || !strings.Contains(f.State["charges"], `refunded: 1200`) {
		t.Errorf("the failure as data: %s", d.JSON())
	}
}

// A capture the code refused where the model takes it: the report says
// the answer the model gives that request (the same id and amount, its
// result "ok") and what it would change, and every example of the
// actions enabled instead is the model's, labelled so, with its examples
// behind -v; the invariants it leaves unchecked are one line.
func TestOnCallRejectionReport(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	c, err := NewChecker("testdata/oncall.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := c.Check(context.Background(), oncallRun("authorize ch_1 ok", "authorize ch_2 ok", "capture ch_1 1000 invalid_state", "refund ch_2 700 ok"))
	if err != nil {
		t.Fatal(err)
	}
	sum := rep.Summary()
	t.Logf("%s", sum)
	for _, want := range []string{
		"record day.jsonl:4 is not a transition of the model from the state reached\n",
		"record:   day.jsonl:4, 2026-10-02T09:00:02Z, process payments-1\n",
		`the model expects: capture(id = "ch_1", amount = 1000, result = "ok"), which changes:`,
		`charges["ch_1"].captured: 0 -> 1000`,
		`charges["ch_1"].status: "authorized" -> "captured"`,
		"enabled instead (the model's moves from this state, not records): ",
		"invariants not checked (2: refundsWithinCapture, capturedAuthorized): the run does not conform",
	} {
		if !strings.Contains(sum, want) {
			t.Errorf("the report lacks %q:\n%s", want, sum)
		}
	}
	if strings.Contains(sum, "e.g.") || strings.Contains(sum, "invariant refundsWithinCapture:") {
		t.Errorf("examples of the model's moves, or an invariant's own not-checked line, without -v:\n%s", sum)
	}
	// -v's example of an action enabled many ways is the one closest to
	// the record: the refused capture's own id and amount
	if v := rep.VerboseSummary(); !strings.Contains(v, `capture (4 ways, e.g. capture(id = "ch_1", amount = 1000, result = "ok"))`) {
		t.Errorf("the verbose report's example of capture:\n%s", v)
	}
	w := rep.Actors[0].Failure.Why
	if w == nil || w.Expected != `capture(id = "ch_1", amount = 1000, result = "ok")` || len(w.Changes) != 2 {
		t.Errorf("the explanation: %+v", w)
	}
}

// The diff walks into records, maps (keys added and removed), sets
// (elements), lists of one length and variants of one label; anything
// else is the whole value's change.
func TestStateDiff(t *testing.T) {
	t.Parallel()
	rec := func(c, r int64, s string) qeval.Value {
		return qeval.NewRec([]string{"captured", "refunded", "status"}, []qeval.Value{qeval.Int(c), qeval.Int(r), qeval.Str(s)})
	}
	a := qeval.State{
		"m::charges": qeval.NewMap([]qeval.Value{qeval.Str("a"), qeval.Str("b")}, []qeval.Value{rec(1, 0, "x"), rec(2, 0, "y")}),
		"m::seen":    qeval.NewSet([]qeval.Value{qeval.Int(1), qeval.Int(2)}),
		"m::log":     qeval.List{qeval.Int(1), qeval.Int(2)},
		"m::n":       qeval.Int(3),
		"m::v":       qeval.Variant{Label: "Some", Val: qeval.Int(1)},
	}
	b := qeval.State{
		"m::charges": qeval.NewMap([]qeval.Value{qeval.Str("a"), qeval.Str("c")}, []qeval.Value{rec(1, 5, "x"), rec(0, 0, "z")}),
		"m::seen":    qeval.NewSet([]qeval.Value{qeval.Int(2), qeval.Int(3)}),
		"m::log":     qeval.List{qeval.Int(1), qeval.Int(2), qeval.Int(3)},
		"m::n":       qeval.Int(3),
		"m::v":       qeval.Variant{Label: "Some", Val: qeval.Int(2)},
	}
	got := stateDiff(a, b, func(n string) string { return strings.TrimPrefix(n, "m::") })
	var lines []string
	for _, c := range got {
		lines = append(lines, c.String())
	}
	want := []string{
		`charges["a"].refunded: 0 -> 5`,
		`charges["b"]: removed { captured: 2, refunded: 0, status: "y" }`,
		`charges["c"]: added { captured: 0, refunded: 0, status: "z" }`,
		`log: [1, 2] -> [1, 2, 3]`,
		`seen: removed 1`,
		`seen: added 3`,
		`v.Some: 1 -> 2`,
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("the diff:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if d := stateDiff(a, a, func(n string) string { return n }); len(d) != 0 {
		t.Errorf("a state against itself: %v", d)
	}
}

// A record is named where it was read, else by its recorder and seq; a
// span's names its trace too, and a node identity's recorder its pod.
func TestRecordID(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 2, 9, 0, 0, 5e8, time.UTC)
	for _, c := range []struct {
		s        qtrace.Step
		id, line string
	}{
		{qtrace.Step{Source: "day.jsonl:12", Recorder: "p", Process: "p", Seq: 11, Time: at}, "day.jsonl:12", "day.jsonl:12, 2026-10-02T09:00:00.5Z, process p"},
		{qtrace.Step{Recorder: "counters/counters-0/fc95", Process: "counters", Seq: 3}, "counters/counters-0/fc95#3",
			"counters/counters-0/fc95#3, process counters, recorder counters/counters-0/fc95 (node counters-0, incarnation fc95)"},
		{qtrace.Step{Recorder: "grist/counters/t1", Process: "counters", Seq: 3}, "grist/counters/t1#3", "grist/counters/t1#3, process counters, recorder grist/counters/t1"},
		{qtrace.Step{Source: qtrace.SpanSource("5b8a", "cf2a"), Recorder: "r", Seq: 1}, "span 5b8a", "span 5b8a, recorder r, trace cf2a"},
		{qtrace.Step{Source: qtrace.SpanSource("5b8a", ""), Recorder: "r", Seq: 1}, "span 5b8a", "span 5b8a, recorder r"},
	} {
		if id := RecordID(&c.s); id != c.id {
			t.Errorf("RecordID(%+v) = %q, want %q", c.s, id, c.id)
		}
		if l := recordWhere(&c.s, 0, false); l != c.line {
			t.Errorf("recordWhere(%+v) = %q, want %q", c.s, l, c.line)
		}
	}
}

// The document's renderings: JUnit has a test case per check, a failure
// carrying its text and a check with no verdict skipped; SARIF a result
// per failure at its record's file and line.
func TestDocumentRenderings(t *testing.T) {
	t.Parallel()
	d := NewDocument("validate")
	d.Binding = "payments.binding.yaml"
	d.Actors = []DocActor{{Actor: "payments", Verdict: "fail", Reason: "violates r", Conformance: "pass",
		Invariants: []DocInvariant{{Name: "r", Kind: "invariant", Verdict: "fail"}, {Name: "q", Kind: "invariant", Verdict: "n/a", Cause: "lost steps"}},
		Failures: []DocFailure{{Check: "r", Kind: "invariant", Verdict: "fail", At: "record day.jsonl:12", Text: "violated after record day.jsonl:12\n  more\n",
			Record: &DocRecord{ID: "day.jsonl:12", Source: "logs/day.jsonl:12", Time: "2026-10-02T09:00:31Z", TraceID: "cf2a"}}}}}
	var j struct {
		Tests    int `xml:"tests,attr"`
		Failures int `xml:"failures,attr"`
		Skipped  int `xml:"skipped,attr"`
		Cases    []struct {
			Name    string `xml:"name,attr"`
			Failure *struct {
				Message string `xml:"message,attr"`
				Body    string `xml:",chardata"`
			} `xml:"failure"`
		} `xml:"testcase"`
	}
	if err := xml.Unmarshal(d.JUnit(), &j); err != nil {
		t.Fatal(err)
	}
	if j.Tests != 3 || j.Failures != 1 || j.Skipped != 1 || j.Cases[1].Failure == nil || j.Cases[1].Failure.Message != "violated after record day.jsonl:12" ||
		!strings.Contains(j.Cases[1].Failure.Body, "more") {
		t.Errorf("JUnit:\n%s", d.JUnit())
	}
	var s struct {
		Version string `json:"version"`
		Runs    []struct {
			Results []struct {
				RuleID    string         `json:"ruleId"`
				Props     map[string]any `json:"properties"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct{ URI string }    `json:"artifactLocation"`
						Region           struct{ StartLine int } `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(d.SARIF(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Version != "2.1.0" || len(s.Runs) != 1 || len(s.Runs[0].Results) != 1 {
		t.Fatalf("SARIF:\n%s", d.SARIF())
	}
	r := s.Runs[0].Results[0]
	if l := r.Locations[0].PhysicalLocation; r.RuleID != "violates" || l.ArtifactLocation.URI != "logs/day.jsonl" || l.Region.StartLine != 12 || r.Props["traceId"] != "cf2a" {
		t.Errorf("SARIF's result:\n%s", d.SARIF())
	}
	var back Document
	if err := json.Unmarshal(d.JSON(), &back); err != nil || back.Format != ReportFormat || back.Actors[0].Failures[0].Record.ID != "day.jsonl:12" {
		t.Errorf("the JSON does not read back (%v):\n%s", err, d.JSON())
	}
}
