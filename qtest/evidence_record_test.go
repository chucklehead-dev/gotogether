package qtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/navproto"
)

func TestNavCapturedRecordEnvironment(t *testing.T) {
	RequireQuint(t)
	out := filepath.Join(t.TempDir(), "node.json")
	t.Setenv("GOTOGETHER_RUN", "TestNavCapturedRecordEnvironment#1:schedule=")
	t.Setenv("GOTOGETHER_NAV", "start")
	t.Setenv("GOTOGETHER_NAV_OUT", out)
	t.Setenv("GOTOGETHER_NAV_RECORD", `{"actor":"account","recorder":"capture","seq":1}`)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(ch qenv.Chooser) error {
		sim := qenv.New(ch)
		defer sim.Close()
		qobs.From(sim.Context()).Add(qtrace.Step{Actor: "account", Recorder: "capture", Seq: 1, Action: "deposit", Args: map[string]any{"amount": 1}, Obs: map[string]any{"balance": 1}})
		return nil
	}}
	sys.Exhaust(t, Bound{Delays: 1, Parallel: 1}).Expect(t, Conforms())
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var node navproto.Node
	if err := json.Unmarshal(b, &node); err != nil {
		t.Fatal(err)
	}
	if node.Record == nil || node.Record.Action != "deposit" || node.Record.Reference.Seq != 1 {
		t.Fatalf("one-shot environment lost record: %s", b)
	}
}

func TestNavCapturedRecordInspection(t *testing.T) {
	RequireQuint(t)
	run := func(ch qenv.Chooser) error {
		sim := qenv.New(ch)
		defer sim.Close()
		qobs.From(sim.Context()).Add(qtrace.Step{Actor: "account", Recorder: "capture", Seq: 1, Action: "deposit", Args: map[string]any{"amount": 1}, Obs: map[string]any{"balance": 1}})
		return nil
	}
	rs := serveAt(t, "TestNavCapturedRecordInspection#1:schedule=", run,
		`{"node":"start"}`,
		`{"node":"start","record":{"actor":"account","recorder":"capture","seq":1}}`,
		`{"node":"start","record":{"actor":"other","recorder":"capture","seq":1}}`,
		`{"node":"start","save":true,"record":{"actor":"account","recorder":"capture","seq":1}}`,
		`{"node":"start"}`)
	if len(rs) != 6 {
		t.Fatalf("responses: %+v", rs)
	}
	base, inspected := rs[1].Node, rs[2].Node
	if inspected == nil || inspected.Record == nil || inspected.Record.Action != "deposit" || string(inspected.Record.Arguments) != `{"amount":1}` {
		t.Fatalf("missing exact captured data: %+v", rs[2])
	}
	if base.ID != inspected.ID || base.Receipt.Status != inspected.Receipt.Status || len(base.Model) != len(inspected.Model) {
		t.Fatal("inspection changed cursor or judgment")
	}
	if !strings.Contains(rs[3].Error, "missing") || !strings.Contains(rs[4].Error, "without") || rs[5].Node == nil {
		t.Fatalf("invalid inspection must fail without ending session: %+v", rs)
	}
}
