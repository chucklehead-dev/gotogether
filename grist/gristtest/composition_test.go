package gristtest_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func compositionChecker(t *testing.T, actors, protocolGuard string, eventually ...string) *validate.Checker {
	t.Helper()
	qtest.RequireQuint(t)
	dir := t.TempDir()
	model := `module composition {
 var n: int
 action init = n' = 0
 action place = n' = n + 1
 action pay = n' = n + 1
 action bankPay(result: str): bool = n' = n + 1
 action shopPlace = all { ` + protocolGuard + `, n' = n + 1 }
 action step = any { place, pay, bankPay("ok"), shopPlace }
 val sane = n >= 0
 val complete = n > 0
}`
	yaml := `actors: ['` + actors + `']
spec: model.qnt
module: composition
outcomes:
  ok: '"ok"'
  closed: '"closed"'
actions:
  place: {}
  pay: {}
  bankPay:
    args: {result: '{{.outcome}}'}
  shopPlace: {}
invariants: [sane]
`
	if len(eventually) > 0 {
		yaml += "eventually: [complete]\n"
	}
	if slices.Contains(eventually, "diagnostic") {
		model = strings.Replace(model, "val complete = n > 0", `val outstanding = if (n > 0) Set({kind: "completion", subject: "order-a", intent: "request-1", expected: "a modeled answer"}) else Set()
 /// gotogether: diagnostic outstanding
 val complete = outstanding.size() == 0`, 1)
	}

	for name, src := range map[string]string{"model.qnt": model, "binding.yaml": yaml} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := validate.NewChecker(filepath.Join(dir, "binding.yaml"), validate.Options{NoITF: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func compositionPlan(t *testing.T, apps []*grist.App) gristtest.Composition {
	t.Helper()
	c := gristtest.Composition{}
	for _, app := range apps {
		c.Models = append(c.Models, gristtest.LocalModel{App: app, Checker: compositionChecker(t, app.Name+"/*", "true")})
	}
	return c
}

func TestCompositionSameHistoryAndOptionalProtocol(t *testing.T) {
	shop, bank, native := protocolPair()
	c := compositionPlan(t, []*grist.App{shop, bank})
	// The complete recorded call agrees with its peer's committed answer.
	native[2].Invocation = false
	native[2].Outcome = "ok"
	got, err := c.Assess(t.Context(), native)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Local) != 2 || len(got.Peers) != 1 || got.Protocol.Configured || got.Protocol.Status != receipt.NotChecked || !strings.HasPrefix(got.History, "sha256:") {
		t.Fatalf("%+v", got)
	}
	for _, r := range got.Local {
		if !r.Applicable || r.Status != receipt.Pass || r.Binding == "" || r.Judgment == nil {
			t.Fatalf("%+v", r)
		}
	}
	if got.Peers[0].Status != receipt.Pass {
		t.Fatalf("%+v", got.Peers)
	}
	// Order the registration differently: its rows and captured-input identity
	// remain deterministic, independent of declaration order.
	slices.Reverse(c.Models)
	again, err := c.Assess(t.Context(), native)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(again)
	if string(a) != string(b) {
		t.Fatalf("registration order changed result:\n%s\n%s", a, b)
	}
	changed := slices.Clone(native)
	changed[2].Outcome = "closed"
	bad, err := c.Assess(t.Context(), changed)
	if err != nil || bad.History == got.History || bad.Peers[0].Status != receipt.Fail {
		t.Fatalf("changed answer: %+v %v", bad, err)
	}
	// Local conformance alone must not hide this cross-service mismatch.
	for _, r := range bad.Local {
		if r.Status != receipt.Pass {
			t.Fatalf("fixture local model failed: %+v", r)
		}
	}
	if status, _ := bad.Problem(); status != receipt.Fail {
		t.Fatalf("mismatch did not fail composition: %+v", bad)
	}
}

func TestCompositionUniversalScope(t *testing.T) {
	native, apps, _, _ := orderFixture("independent")
	c := compositionPlan(t, apps)
	c.Protocol = &gristtest.ProtocolModel{Checker: compositionChecker(t, "protocol/*", "true"), Limit: 6}
	good, err := c.Assess(t.Context(), native)
	if err != nil || good.Protocol.Status != receipt.Pass || !good.Protocol.Exhaustive || good.Protocol.Checked != 6 || good.Protocol.Judgment == nil {
		t.Fatalf("exhaustive: %+v %v", good, err)
	}
	if good.Protocol.Quantifier != "all_causal_orders" || len(good.Protocol.Counterexample) != 0 {
		t.Fatalf("%+v", good.Protocol)
	}
	c.Protocol.Limit = 1
	limited, err := c.Assess(t.Context(), native)
	if err != nil || limited.Protocol.Status != receipt.Inconclusive || limited.Protocol.Exhaustive || limited.Protocol.Checked != 1 || len(limited.Protocol.Counterexample) != 0 {
		t.Fatalf("limited: %+v %v", limited, err)
	}
	// The canonical extension starts with bank: it passes this stronger model.
	// A later allowed extension starts with shop, exposing a counterexample.
	c.Protocol = &gristtest.ProtocolModel{Checker: compositionChecker(t, "protocol/*", "n > 0"), Limit: 6}
	bad, err := c.Assess(t.Context(), native)
	if err != nil || bad.Protocol.Status != receipt.Fail || bad.Protocol.Checked < 2 || len(bad.Protocol.Counterexample) == 0 {
		t.Fatalf("noncanonical counterexample: %+v %v", bad, err)
	}
	for _, r := range bad.Protocol.Counterexample {
		if !strings.Contains(r.ID, "grist/") || !strings.Contains(r.ID, "seq") || r.Actor != "protocol/t1" || r.Action == "" {
			t.Fatalf("lost native record identity: %+v", r)
		}
	}
	for _, r := range bad.Local {
		if r.Status != receipt.Pass {
			t.Fatalf("local model implied protocol correctness: %+v", r)
		}
	}
}

func TestCompositionInactiveAndOrphanedEdges(t *testing.T) {
	shop, bank, native := protocolPair()
	c := compositionPlan(t, []*grist.App{shop, bank})
	// No call and no caller-owned peer commit: the edge is unexercised.
	lone := []qtrace.Step{native[0], native[3]}
	got, err := c.Assess(t.Context(), lone)
	if err != nil || got.Peers[0].Applicable || got.Peers[0].Status != receipt.NotChecked || got.Local[0].Applicable {
		t.Fatalf("inactive: %+v %v", got, err)
	}
	// But an orphan peer commit is work even when every call record is missing.
	// It must not be hidden behind an unexercised edge badge.
	orphan := []qtrace.Step{native[0], native[1], native[3]}
	bad, err := c.Assess(t.Context(), orphan)
	if err != nil || !bad.Peers[0].Applicable || bad.Peers[0].Status != receipt.Fail {
		t.Fatalf("orphan hidden: %+v %v", bad, err)
	}
	empty, err := c.Assess(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := empty.Problem(); status != receipt.NotChecked {
		t.Fatalf("empty history passed: %+v", empty)
	}
	c.Protocol = &gristtest.ProtocolModel{Checker: compositionChecker(t, "protocol/*", "true"), Limit: 6}
	empty, err = c.Assess(t.Context(), nil)
	if err != nil || empty.Protocol.Applicable || empty.Protocol.Status != receipt.NotChecked || empty.Protocol.Checked != 0 {
		t.Fatalf("empty universal check passed: %+v %v", empty, err)
	}
}

func TestCompositionErrorsAreNotCounterexamples(t *testing.T) {
	shop, bank, native := protocolPair()
	c := compositionPlan(t, []*grist.App{shop, bank})
	c.Protocol = &gristtest.ProtocolModel{Checker: compositionChecker(t, "protocol/*", "true"), Limit: 6}
	// A gap in native storage prevents projection; it is not a model defect.
	corrupt := slices.Clone(native)
	corrupt[1].Seq = 2
	bad, err := c.Assess(t.Context(), corrupt)
	if err == nil || bad.Error == "" || bad.Protocol.Status != receipt.Error || len(bad.Protocol.Counterexample) != 0 {
		t.Fatalf("evidence error: %+v %v", bad, err)
	}
	if bad.Peers[0].Status != receipt.Inconclusive {
		t.Fatalf("missing evidence became a link counterexample: %+v", bad.Peers)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stopped, err := c.Assess(ctx, native)
	if err == nil || stopped.Error == "" || len(stopped.Protocol.Counterexample) != 0 {
		t.Fatalf("cancellation became a counterexample: %+v %v", stopped, err)
	}
	// The checker really judges bank, but the registration claims shop.
	wrong := c
	wrong.Models = slices.Clone(c.Models)
	wrong.Models[0].Checker = c.Models[1].Checker
	result, err := wrong.Assess(t.Context(), native)
	if err == nil || result.Error == "" {
		t.Fatalf("wrong App model labeled pass: %+v %v", result, err)
	}
	// Registering a caller cannot silently omit its declared peer model.
	missing := gristtest.Composition{Models: c.Models[:1]}
	result, err = missing.Assess(t.Context(), native)
	if err == nil || !strings.Contains(result.Error, "register that App") {
		t.Fatalf("missing peer: %+v %v", result, err)
	}
}

func TestCompositionSystemDoesNotDiscardAssessment(t *testing.T) {
	c := gristtest.Composition{}
	sys := qtest.System{Binding: "binding.yaml"}
	attached := c.System(sys)
	if attached.Assess == nil || attached.Binding != sys.Binding {
		t.Fatal("composition was not registered on the existing system")
	}
	defer func() {
		p := recover()
		if p == nil || !strings.Contains(p.(string), "already has an assessment") {
			t.Fatalf("existing checks discarded: %v", p)
		}
	}()
	c.System(attached)
}

func TestCompositionInconclusiveProtocolIsNotCounterexample(t *testing.T) {
	qtest.RequireQuint(t)
	native, apps, _, _ := orderFixture("independent")
	c := compositionPlan(t, apps)
	dir := t.TempDir()
	model := `module blocked {
 var n:int
 action init = n' = 0
 action tick = all {n < 5, n' = n + 1}
 action shopPlace = all {n == 5, n' = n}
 action bankPay = all {n == 5, n' = n}
 action step = any {tick, shopPlace, bankPay}
}`
	yaml := `actors: ['protocol/*']
spec: blocked.qnt
module: blocked
hidden: [tick]
hiddenDepth: 4
actions:
  shopPlace: {}
  bankPay: {}
`
	for name, src := range map[string]string{"blocked.qnt": model, "binding.yaml": yaml} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	checker, err := validate.NewChecker(filepath.Join(dir, "binding.yaml"), validate.Options{NoITF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	c.Protocol = &gristtest.ProtocolModel{Checker: checker, Limit: 6}
	result, err := c.Assess(t.Context(), native)
	if err != nil || result.Error != "" || result.Protocol.Status != receipt.Inconclusive || len(result.Protocol.Counterexample) != 0 || result.Protocol.Judgment == nil {
		t.Fatalf("inconclusive became a counterexample: %+v %v", result, err)
	}
}

func TestCompositionRequiresDeclaredCompletionToBeJudged(t *testing.T) {
	shop, bank, native := protocolPair()
	native = slices.DeleteFunc(slices.Clone(native), func(s qtrace.Step) bool { return s.Settled })
	c := compositionPlan(t, []*grist.App{shop, bank})
	c.Models[1].Checker = compositionChecker(t, "bank/*", "true", "complete")
	local, err := c.Assess(t.Context(), native)
	if err != nil {
		t.Fatal(err)
	}
	if local.Local[0].Status != receipt.Inconclusive || local.Local[0].Judgment == nil || local.Local[0].Judgment.Status != receipt.Pass || !strings.Contains(local.Local[0].Reason, "eventually") {
		t.Fatalf("local completion was silently unjudged: %+v", local)
	}
	c = compositionPlan(t, []*grist.App{shop, bank})
	c.Protocol = &gristtest.ProtocolModel{Checker: compositionChecker(t, "protocol/*", "true", "complete"), Limit: 6}
	protocol, err := c.Assess(t.Context(), native)
	if err != nil || protocol.Protocol.Status != receipt.Inconclusive || protocol.Protocol.Exhaustive || len(protocol.Protocol.Counterexample) != 0 || protocol.Protocol.Judgment == nil {
		t.Fatalf("protocol completion was silently unjudged: %+v %v", protocol, err)
	}
}

func TestCompositionRejectsClientViewAsNativeLocalModel(t *testing.T) {
	shop, bank, native := protocolPair()
	native = append(slices.Clone(native), qtrace.Step{Recorder: "client", Seq: 1, Actor: "shop/t1/clients", Action: "place"})
	c := compositionPlan(t, []*grist.App{shop, bank})
	c.Models[0].Checker = compositionChecker(t, "shop/*/clients", "true")
	result, err := c.Assess(t.Context(), native)
	if err == nil || !strings.Contains(result.Error, "native actors") {
		t.Fatalf("client-only binding substituted for native local model: %+v %v", result, err)
	}
	c.Models[0].Checker = compositionChecker(t, "typo/*", "true")
	result, err = c.Assess(t.Context(), native)
	if err == nil || !strings.Contains(result.Error, "selected no actor") {
		t.Fatalf("misspelled actor selection hid recorded work: %+v %v", result, err)
	}
}

func TestCompositionProtocolKeepsModelAuthoredDiagnostics(t *testing.T) {
	native, apps, _, _ := orderFixture("independent")
	c := compositionPlan(t, apps)
	c.Protocol = &gristtest.ProtocolModel{Checker: compositionChecker(t, "protocol/*", "true", "diagnostic"), Limit: 6}
	result, err := c.Assess(t.Context(), native)
	if err != nil || result.Protocol.Status != receipt.Fail || result.Protocol.Judgment == nil {
		t.Fatalf("completion failure: %+v %v", result, err)
	}
	actors := result.Protocol.Judgment.Actors
	if len(actors) != 1 || len(actors[0].Diagnostics) != 1 {
		t.Fatalf("model explanation was lost: %+v", actors)
	}
	d := actors[0].Diagnostics[0]
	if d.Actor != "protocol/t1" || d.Property != "complete" || d.Projection != "outstanding" || d.Cause != "defect" || d.Evidence != "unresolved" {
		t.Fatalf("diagnostic scope changed: %+v", d)
	}
	if os.Getenv("QUINT_BACKEND") == "typescript" {
		if d.Status != "unavailable" || d.Reason == "" {
			t.Fatalf("backend's missing exact state was hidden: %+v", d)
		}
	} else if d.Status != "available" || len(d.Items) != 1 || d.Items[0].Intent != "request-1" {
		t.Fatalf("business intent was lost: %+v", d)
	}
	raw, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(raw), `"diagnostics"`) || !strings.Contains(result.String(), "complete → outstanding") {
		t.Fatalf("explanation missing from JSON/text: %s %v", raw, err)
	}
}

// One registration is shared by parallel seeded runs. A different run's peer
// outcome must never replace the judgment or identity of this captured history.
func TestCompositionConcurrentHistoriesKeepTheirOwnResults(t *testing.T) {
	shop, bank, native := protocolPair()
	native[2].Invocation = false
	native[2].Outcome = "ok"
	c := compositionPlan(t, []*grist.App{shop, bank})
	altered := slices.Clone(native)
	altered[2].Outcome = "closed"
	type result struct {
		value receipt.Composition
		err   error
		want  receipt.Status
	}
	done := make(chan result, 2)
	for _, tc := range []struct {
		steps []qtrace.Step
		want  receipt.Status
	}{{native, receipt.Pass}, {altered, receipt.Fail}} {
		go func() { v, err := c.Assess(t.Context(), tc.steps); done <- result{v, err, tc.want} }()
	}
	histories := map[string]bool{}
	for range 2 {
		got := <-done
		if got.err != nil || len(got.value.Peers) != 1 || got.value.Peers[0].Status != got.want {
			t.Fatalf("concurrent history crossed results: %+v %v", got.value, got.err)
		}
		histories[got.value.History] = true
	}
	if len(histories) != 2 {
		t.Fatal("different concurrent inputs shared an identity")
	}
}
