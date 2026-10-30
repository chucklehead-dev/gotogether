package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
	"github.com/chucklehead-dev/gotogether/validate/navproto"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

type evidenceRun struct {
	node     *navproto.Node
	record   *diagnostic.RecordDetail
	requests []navproto.Request
}

func (r *evidenceRun) Node(req navproto.Request) (*navproto.Node, error) {
	r.requests = append(r.requests, req)
	if req.Record != nil {
		return &navproto.Node{ID: req.Node, Record: r.record}, nil
	}
	return r.node, nil
}

func obligation(matches ...diagnostic.Match) diagnostic.Result {
	return diagnostic.Result{Property: "allAnswered", Scope: "settled_checked_history", Items: []diagnostic.Item{{Kind: "cancellation", Subject: "order-a", Intent: "cancel-1", Expected: "inventory cancellation answer", Evidence: diagnostic.Evidence{Status: "resolved", Matches: matches}}}}
}

func TestRunCompositionEvidenceInspector(t *testing.T) {
	ref := diagnostic.RecordRef{Actor: "checkout/t1", Recorder: "checkout-t1", Seq: 17}
	detail := obligation(diagnostic.Match{Actor: "protocol/t1", At: 2, Record: ref, Origin: ref})
	node := &navproto.Node{ID: "0.1@3", Run: "sample", Choice: &navproto.Choice{Taken: 1}, Branches: []navproto.Branch{{Option: 0}, {Option: 1}}, Receipt: receipt.Summary{Composition: &receipt.Composition{Protocol: receipt.Protocol{Component: receipt.Component{Target: "business", Judgment: &receipt.History{Actors: []receipt.Actor{{Actor: "protocol/t1", Diagnostics: []diagnostic.Result{detail}}}}}}}}}
	run := &evidenceRun{node: node, record: &diagnostic.RecordDetail{Reference: ref, Action: "cancel", Outcome: "ok", Arguments: json.RawMessage(`{"id":"cancel-1","units":9007199254740993}`), Observations: json.RawMessage(`{"head":12}`)}}
	d := drive(t, Config{Run: run})
	before := *d.m.cur()
	d.keys("e")
	if text := d.m.Render(); !strings.Contains(text, "composition · business") || !strings.Contains(text, "navigation cursors unchanged") {
		t.Fatalf("missing composition evidence scope:\n%s", text)
	}
	d.keys("enter")
	if len(run.requests) != 2 || run.requests[1].Node != node.ID || run.requests[1].Record == nil || *run.requests[1].Record != ref {
		t.Fatalf("record request moved schedule or lost identity: %+v", run.requests)
	}
	text := d.m.Render()
	for _, want := range []string{"Captured record · no model judgment or cursor move", "action: cancel", "cancel-1", "9007199254740993", "seq 17"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	d.keys("backspace", "backspace")
	if len(d.m.stack) != 1 || d.m.cur().r != before.r || d.m.cur().r.ID != node.ID || d.m.cur().cursor != before.cursor {
		t.Fatal("inspection changed the navigation cursor")
	}
}

type evidenceModel struct {
	node      *validate.Node
	records   map[uint64]*diagnostic.RecordDetail
	requested []diagnostic.RecordRef
}

func (n *evidenceModel) Node(...string) (*validate.Node, error) { return n.node, nil }
func (n *evidenceModel) Fold(...string) (*validate.Node, error) { return n.node, nil }
func (n *evidenceModel) Record(ref diagnostic.RecordRef) (*diagnostic.RecordDetail, error) {
	n.requested = append(n.requested, ref)
	if record := n.records[ref.Seq]; record != nil {
		return record, nil
	}
	return nil, fmt.Errorf("record is missing from this capture")
}

func TestProjectedAndNativeEvidenceAreExplicitChoices(t *testing.T) {
	projected := diagnostic.RecordRef{Actor: "protocol/t1", Recorder: "protocol-t1", Seq: 2}
	native := diagnostic.RecordRef{Actor: "checkout/t1", Recorder: "checkout-t1", Seq: 91}
	nav := &evidenceModel{node: &validate.Node{Diagnostics: []diagnostic.Result{obligation(diagnostic.Match{Record: projected, Origin: native})}}, records: map[uint64]*diagnostic.RecordDetail{2: {Reference: projected, Action: "checkoutCancel"}}}
	d := drive(t, Config{Model: nav})
	d.keys("e")
	text := d.m.Render()
	if !strings.Contains(text, "projected record") || !strings.Contains(text, "native origin") {
		t.Fatalf("projection/origin choices hidden:\n%s", text)
	}
	d.keys("enter")
	if d.m.cur().record == nil || d.m.cur().recordKind != "projected record" {
		t.Fatal("projected choice was not inspected explicitly")
	}
	d.keys("backspace", "down", "enter")
	if d.m.cur().evidence == nil || !strings.Contains(d.m.Status(), "missing from this capture") {
		t.Fatalf("missing native record did not remain explicit: %s", d.m.Status())
	}
	if len(nav.requested) != 2 || nav.requested[0] != projected || nav.requested[1] != native {
		t.Fatalf("silently selected another record: %+v", nav.requested)
	}
}

func TestEvidenceMissingAndAmbiguousRemainInspectable(t *testing.T) {
	detail := obligation()
	detail.Items[0].Evidence = diagnostic.Evidence{Status: "missing", Reason: "no matching command"}
	ambiguous := detail.Items[0]
	ambiguous.Intent = "cancel-2"
	a := diagnostic.RecordRef{Actor: "checkout/t1", Recorder: "node", Seq: 3}
	b := a
	b.Seq = 4
	ambiguous.Evidence = diagnostic.Evidence{Status: "ambiguous", Reason: "two matching commands", Matches: []diagnostic.Match{{Record: a, Origin: a}, {Record: b, Origin: b}}}
	detail.Items = append(detail.Items, ambiguous)
	nav := &evidenceModel{node: &validate.Node{Diagnostics: []diagnostic.Result{detail}}, records: map[uint64]*diagnostic.RecordDetail{3: {Reference: a}, 4: {Reference: b}}}
	d := drive(t, Config{Model: nav})
	d.keys("e", "enter")
	if len(nav.requested) != 0 || !strings.Contains(d.m.Status(), "missing: no matching command") {
		t.Fatal("missing evidence fabricated a record lookup")
	}
	text := d.m.Render()
	for _, want := range []string{"[ambiguous]", "candidate 1/2", "candidate 2/2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	d.keys("down", "down", "enter")
	if len(nav.requested) != 1 || nav.requested[0].Seq != 4 {
		t.Fatalf("did not inspect explicitly selected candidate: %+v", nav.requested)
	}
}

func TestTerminalDiagnosticRecordForwarding(t *testing.T) {
	ref := diagnostic.RecordRef{Actor: "tenant", Recorder: "node", Seq: 1}
	nav := &evidenceModel{records: map[uint64]*diagnostic.RecordDetail{1: {Reference: ref}}}
	overlay := terminalDiagnosticNav{ModelNav: nav}
	got, err := overlay.Record(ref)
	if err != nil || got.Reference != ref {
		t.Fatalf("overlay lost record lookup: %v, %v", got, err)
	}
}

func TestEvidenceCandidateOmissionIsVisible(t *testing.T) {
	ref := diagnostic.RecordRef{Actor: "tenant", Recorder: "node", Seq: 1}
	detail := obligation(diagnostic.Match{Record: ref, Origin: ref})
	detail.Items[0].Evidence.Status = "ambiguous"
	detail.Items[0].Evidence.Total = 40
	detail.Items[0].Evidence.Omitted = 39
	nav := &evidenceModel{node: &validate.Node{Diagnostics: []diagnostic.Result{detail}}}
	d := drive(t, Config{Model: nav})
	d.keys("e")
	for _, want := range []string{"candidate 1/40", "39 of 40 matching records omitted", "[ambiguous]"} {
		if !strings.Contains(d.m.Render(), want) {
			t.Fatalf("missing %q:\n%s", want, d.m.Render())
		}
	}
}
