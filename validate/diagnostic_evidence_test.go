package validate

import (
	"encoding/json"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
)

func evidenceChecker() *Checker {
	return &Checker{Binding: &binding.Binding{DiagnosticEvidence: map[string]map[string]binding.EvidenceRule{"outstanding": {"cancel": {Action: "cancel", Match: map[string]string{"id": "intent", "order": "subject"}}}}}}
}
func evidenceDetail() diagnostic.Result {
	return diagnostic.Result{Actor: "protocol/t1", Projection: "outstanding", Items: []diagnostic.Item{{Kind: "cancel", Intent: "i1", Subject: "o1"}}}
}
func evidenceCall(id, order string, seq uint64) binding.Call {
	return binding.Call{Action: "cancel", Args: map[string]string{"id": id, "order": order}, Step: &qtrace.Step{Actor: "protocol/t1", Recorder: "projected", Seq: seq, Args: map[string]any{"id": "raw-id-not-the-model-id"}, Source: "not an identity"}}
}
func TestDiagnosticEvidenceMatchesBoundCallsWithinActor(t *testing.T) {
	c := evidenceChecker()
	for _, tc := range []struct {
		name, actor, status string
		calls               []binding.Call
	}{
		{"one", "protocol/t1", "resolved", []binding.Call{evidenceCall(`"i1"`, `"o1"`, 3)}},
		{"wrong tenant", "protocol/t2", "unavailable", []binding.Call{evidenceCall(`"i1"`, `"o1"`, 3)}},
		{"wrong subject", "protocol/t1", "missing", []binding.Call{evidenceCall(`"i1"`, `"other"`, 3)}},
		{"wrong intent", "protocol/t1", "missing", []binding.Call{evidenceCall(`"other"`, `"o1"`, 3)}},
		{"ambiguous", "protocol/t1", "ambiguous", []binding.Call{evidenceCall(`"i1"`, `"o1"`, 3), evidenceCall(`"i1"`, `"o1"`, 4)}},
		{"no identity", "protocol/t1", "unavailable", []binding.Call{evidenceCall(`"i1"`, `"o1"`, 0)}},
		{"computed expression", "protocol/t1", "unavailable", []binding.Call{evidenceCall(`str(1)`, `"o1"`, 3)}},
		{"inferred", "protocol/t1", "missing", []binding.Call{{Action: "cancel", Args: map[string]string{"id": `"i1"`, "order": `"o1"`}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := evidenceDetail()
			c.resolveDiagnosticEvidence(&d, &binding.Plan{Actor: tc.actor, Calls: tc.calls})
			e := d.Items[0].Evidence
			if e.Status != tc.status {
				t.Fatalf("evidence %+v", e)
			}
			if e.Status == "resolved" {
				m := e.Matches[0]
				if m.At != 1 || m.Record.Seq != 3 || m.Origin != m.Record || d.Evidence != "resolved" {
					t.Fatalf("wrong identity %+v", d)
				}
			}
		})
	}
	for _, assumed := range []bool{true, false} {
		call := evidenceCall(`"i1"`, `"o1"`, 3)
		call.Step.Ambiguous = assumed
		call.Step.Invocation = !assumed
		d := evidenceDetail()
		c.resolveDiagnosticEvidence(&d, &binding.Plan{Actor: d.Actor, Calls: []binding.Call{call}})
		if d.Items[0].Evidence.Status != "missing" || len(d.Items[0].Evidence.Matches) != 0 {
			t.Fatalf("assumed/pending call became origin: %+v", d)
		}
	}
	d := evidenceDetail()
	d.Projection = "unconfigured"
	c.resolveDiagnosticEvidence(&d, nil)
	if d.Items[0].Evidence.Status != "unconfigured" {
		t.Fatalf("guessed rule %+v", d)
	}
}

func TestDiagnosticEvidenceProjectedOriginAndExactSequence(t *testing.T) {
	c := evidenceChecker()
	r := c.Binding.DiagnosticEvidence["outstanding"]["cancel"]
	r.Origin = &binding.EvidenceOrigin{Actor: `{{index .obs "native.actor"}}`, Recorder: `{{index .obs "native.recorder"}}`, Seq: `{{index .obs "native.seq"}}`}
	c.Binding.DiagnosticEvidence["outstanding"]["cancel"] = r
	call := evidenceCall(`"i1"`, `"o1"`, 2)
	call.Step.Obs = map[string]any{"native.actor": "checkout/t1", "native.recorder": "original", "native.seq": "18446744073709551615"}
	d := evidenceDetail()
	c.resolveDiagnosticEvidence(&d, &binding.Plan{Actor: d.Actor, Calls: []binding.Call{call}})
	e := d.Items[0].Evidence
	if e.Status != "resolved" || e.Matches[0].Record.Seq != 2 || e.Matches[0].Origin.Seq != ^uint64(0) || e.Matches[0].Origin.Actor != "checkout/t1" || e.Matches[0].Origin.Source != "" {
		t.Fatalf("lost identity %+v", e)
	}
	raw, err := json.Marshal(d)
	if err != nil || !strings.Contains(string(raw), `18446744073709551615`) {
		t.Fatalf("JSON %s %v", raw, err)
	}
	delete(call.Step.Obs, "native.seq")
	c.resolveDiagnosticEvidence(&d, &binding.Plan{Actor: d.Actor, Calls: []binding.Call{call}})
	if d.Items[0].Evidence.Status != "unavailable" || len(d.Items[0].Evidence.Matches) != 0 {
		t.Fatalf("guessed native seq from Source: %+v", d)
	}
}

func TestDiagnosticEvidenceTerminalAndObservedPrefix(t *testing.T) {
	skip.Quint(t)
	c, err := NewChecker("testdata/diagnostic/binding.yaml", Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Binding.DiagnosticEvidence = map[string]map[string]binding.EvidenceRule{"outstanding": {"cancellation": {Action: "request", Match: map[string]string{"id": "intent"}}}}
	steps := diagnosticSteps(t, true)
	report, err := c.Check(t.Context(), steps)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range report.Actors {
		d := a.Absent[0].Diagnostic
		if d.Status == "available" {
			for _, item := range d.Items {
				if item.Evidence.Status != "resolved" || item.Evidence.Matches[0].Record.Actor != a.Actor || item.Evidence.Matches[0].Origin.Actor != a.Actor {
					t.Fatalf("terminal cross-actor association: %+v", item)
				}
			}
		}
	}
	nav, err := c.Nav(NavStart{Steps: steps, Actor: "tenant-a", At: 1})
	if err != nil {
		t.Fatal(err)
	}
	node, err := nav.Node()
	if err != nil {
		t.Fatal(err)
	}
	d := node.Diagnostics[0]
	if len(d.Items) != 1 || d.Items[0].Intent != "c1" || d.Items[0].Evidence.Status != "resolved" || d.Items[0].Evidence.Matches[0].At != 1 {
		t.Fatalf("observed prefix %+v", d)
	}
	// c2 occurs later in the capture, but the counterfactual branch has not
	// observed it. A matching business identifier cannot turn it into evidence.
	future, err := nav.Node(`request("c2")`)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range future.Diagnostics[0].Items {
		if item.Evidence.Status != "unavailable" || len(item.Evidence.Matches) != 0 || !strings.Contains(item.Evidence.Reason, "counterfactual") {
			t.Fatalf("linked hypothetical state to captured event: %+v", item)
		}
	}
}

func TestDiagnosticEvidenceBoundsAmbiguousCandidates(t *testing.T) {
	c := evidenceChecker()
	var calls []binding.Call
	for i := range 40 {
		calls = append(calls, evidenceCall(`"i1"`, `"o1"`, uint64(i+1)))
	}
	d := evidenceDetail()
	c.resolveDiagnosticEvidence(&d, &binding.Plan{Actor: d.Actor, Calls: calls})
	e := d.Items[0].Evidence
	if e.Status != "ambiguous" || e.Total != 40 || e.Omitted != 8 || len(e.Matches) != 32 || e.Matches[31].At != 32 {
		t.Fatalf("ambiguous candidates truncated unsafely: %+v", e)
	}
	raw, err := json.Marshal(d)
	if err != nil || !strings.Contains(string(raw), `"omitted":8`) || !strings.Contains(d.String(), "40 candidates; 8 omitted") {
		t.Fatalf("truncation hidden: %s %v", raw, err)
	}
	// A truncated model projection cannot claim all of its evidence resolved.
	d = evidenceDetail()
	d.Omitted = 2
	c.resolveDiagnosticEvidence(&d, &binding.Plan{Actor: d.Actor, Calls: calls[:1]})
	if d.Evidence != "partial" || d.Items[0].Evidence.Status != "resolved" {
		t.Fatalf("omitted obligations claimed resolved: %+v", d)
	}
}
