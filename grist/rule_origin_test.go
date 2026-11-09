package grist_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/gen"
)

type originItem struct {
	Qty int `json:"qty"`
}

// Two guards may share their rejection name. Generated verdicts must identify
// the first refusing rule rather than deriving its identity from that name.
func TestGeneratedRuleRefusalOrigin(t *testing.T) {
	requireQuint(t)
	a := grist.New("origin")
	ev := grist.NewEvent[originItem](a, "held")
	invalid := grist.Rejection("invalid")
	grist.NewCommand(a, "hold", func(_ *grist.Reads, c originItem) ([]grist.Event, error) {
		if c.Qty < 0 {
			return nil, invalid
		}
		return []grist.Event{ev.Of(c)}, nil
	}, grist.Appends(ev), grist.Refuses(invalid), grist.Internal())
	full := grist.Rejection("full")
	grist.NewRule(a, "first", ev, func(_ *grist.Reads, e originItem) error {
		if e.Qty == 1 {
			return full
		}
		return nil
	}, grist.Refuses(full))
	grist.NewRule(a, "second", ev, func(_ *grist.Reads, e originItem) error {
		if e.Qty >= 1 {
			return full
		}
		return nil
	}, grist.Refuses(full))
	dir := t.TempDir()
	files, err := gen.Starter(a)
	if err != nil {
		t.Fatal(err)
	}
	rules := string(files["model/origin_rules.qnt"])
	for old, next := range map[string]string{
		`pure def holdOutcome(log: List[Event], c: HoldArgs): str = "ok"`:      `pure def holdOutcome(log: List[Event], c: HoldArgs): str = if (c.qty < 0) "invalid" else "ok"`,
		`pure def holdEvents(log: List[Event], c: HoldArgs): List[Event] = []`: `pure def holdEvents(log: List[Event], c: HoldArgs): List[Event] = [held(c.qty)]`,
		`pure def firstRefusal(log: List[Event], e: Event): str = ""`:          `pure def firstRefusal(log: List[Event], e: Event): str = if (heldQty(e) == 1) "full" else ""`,
		`pure def secondRefusal(log: List[Event], e: Event): str = ""`:         `pure def secondRefusal(log: List[Event], e: Event): str = if (heldQty(e) >= 1) "full" else ""`,
	} {
		if !strings.Contains(rules, old) {
			t.Fatalf("missing model hole %s", old)
		}
		rules = strings.Replace(rules, old, next, 1)
	}
	files["model/origin_rules.qnt"] = []byte(rules)
	for name, b := range files {
		target := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	generated, err := gen.Generate(a, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := generated.Write(dir); err != nil {
		t.Fatal(err)
	}
	model := filepath.Join(dir, "model/origin.qnt")
	b, err := os.ReadFile(model)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.TrimSpace(string(b))
	source = strings.TrimSuffix(source, "}") + `
 run noRefusalTest = assert(holdRuled([], { qty: 0, caller: "a" }).rule == "")
 run firstRefusalTest = assert(holdRuled([], { qty: 1, caller: "a" }).rule == "first")
 run secondRefusalTest = assert(holdRuled([], { qty: 2, caller: "a" }).rule == "second")
 run sharedRefusalTest = assert(holdRuled([], { qty: 1, caller: "a" }).outcome == "full" and holdRuled([], { qty: 2, caller: "a" }).outcome == "full")
 run decisionRefusalTest = assert(holdRuled([], { qty: -1, caller: "a" }).rule == "" and holdRuled([], { qty: -1, caller: "a" }).decided == "invalid" and holdRuled([], { qty: -1, caller: "a" }).outcome == "invalid")
 }
 `
	if err := os.WriteFile(model, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	quint(t, dir, "test", "model/origin.qnt", "--main", "origin", "--backend", "typescript", "--match", "RefusalTest$", "--max-samples", "1", "--seed", "1")
}
