package gristtest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

type pay struct {
	Order string `json:"order"`
}

// pair is shop calling its peer bank's pay at least once.
func pair() (shop, bank *grist.App) {
	bank = grist.New("bank")
	paid := grist.NewEvent[pay](bank, "paid")
	cmd := grist.NewCommand(bank, "pay", func(r *grist.Reads, c pay) ([]grist.Event, error) {
		return []grist.Event{paid.Of(c)}, nil
	}, grist.Unconditioned("reads nothing"), grist.Refuses(grist.Rejection("closed")), grist.Authorize(grist.Authenticated))
	shop = grist.New("shop")
	ordered := grist.NewEvent[pay](shop, "ordered")
	call := grist.NewPeerCall(grist.NewPeer(shop, bank), cmd)
	grist.NewProcessor(shop, "charge", ordered, func(ctx context.Context, r *grist.Reads, e pay, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error { _, err := call.Do(ctx, e); return err }, grist.AtLeastOnce)
	}, grist.Delivers(grist.AtLeastOnce), grist.Uses(call))
	return shop, bank
}

// Links relates a caller's calls to its peer's commits by key: an answer
// with the peer's step of the key, in the same tenant and of the same
// outcome, passes (an unknown answer or a pending call promises nothing);
// an answer the peer never committed or committed otherwise, a commit
// under an id naming the caller that no call has, a key's two steps at
// the peer and a key reused with other arguments are each a problem, and
// a run with no call at all is one too.
func TestLinks(t *testing.T) {
	shop, bank := pair()
	call := func(key, outcome string) qtrace.Step {
		return qtrace.Step{Action: "bankPay", Actor: "shop/t1", Call: 1, Outcome: outcome, Args: map[string]any{"key": key}}
	}
	pending := qtrace.Step{Action: "bankPay", Actor: "shop/t1", Record: qtrace.Record{Invocation: true}, Args: map[string]any{"key": "shop/charge/t1/3"}}
	commit := func(tenant, id, outcome string) qtrace.Step {
		return qtrace.Step{Action: "pay", Actor: "bank/" + tenant, Outcome: outcome, Args: map[string]any{"id": id}}
	}
	k1, k2 := "shop/charge/t1/1", "shop/charge/t1/2"
	ok := []qtrace.Step{call(k1, "ok"), commit("t1", k1, ""), call(k1, "ok"), call(k2, "closed"), commit("t1", k2, "closed"),
		call("shop/charge/t1/4", "unknown"), pending, commit("t1", "c-1", ""), {Action: "pay", Actor: "bank/t1/clients", Args: map[string]any{"id": "shop/x"}}}
	if p := gristtest.Links(shop, bank, ok); len(p) > 0 {
		t.Errorf("the two sides agree: %v", p)
	}
	for name, c := range map[string]struct {
		steps []qtrace.Step
		want  string
	}{
		"never committed":   {[]qtrace.Step{call(k1, "ok")}, "an answer the peer's commit does not give"},
		"another outcome":   {[]qtrace.Step{call(k1, "ok"), commit("t1", k1, "closed")}, "an answer the peer's commit does not give"},
		"another tenant":    {[]qtrace.Step{call(k1, "ok"), commit("t2", k1, "")}, "an answer the peer's commit does not give"},
		"an id no call has": {[]qtrace.Step{call(k1, "ok"), commit("t1", k1, ""), commit("t1", k1+"/7", "")}, "which shop never called with"},
		"two steps":         {[]qtrace.Step{call(k1, "ok"), commit("t1", k1, ""), commit("t1", k1, "")}, "a key names one outcome"},
		"reused":            {[]qtrace.Step{call(k1, "unknown"), commit("t1", k1, "idempotency-key-reused")}, "sent the key with other arguments"},
		"no call":           {[]qtrace.Step{commit("t1", "c-1", "")}, "no call of shop's to bank"},
	} {
		p := gristtest.Links(shop, bank, c.steps)
		if !strings.Contains(strings.Join(p, "\n"), c.want) {
			t.Errorf("%s: %v, want %q", name, p, c.want)
		}
	}
	if p := gristtest.Links(bank, shop, ok); len(p) != 1 || !strings.Contains(p[0], "bank declares no peer call of shop") {
		t.Errorf("the pair reversed: %v, want the caller named first", p)
	}
	if msgs := run(t, func(tb testing.TB) { gristtest.Linked(tb, shop, bank, []qtrace.Step{call(k1, "ok")}) }); len(msgs) != 1 {
		t.Errorf("Linked: %v, want its one problem", msgs)
	}
}

// A key and outcome alone do not identify a peer call: a transport may
// route pay to another command, both models accepting their own records.
// Links must compare the command even if it too is declared by the caller,
// and must notice caller-owned ids on commands the caller never declared.
func TestLinksWrongCommand(t *testing.T) {
	shop, bank := pair()
	other := grist.NewCommand(bank, "refund", func(r *grist.Reads, c pay) ([]grist.Event, error) { return nil, nil },
		grist.Unconditioned("reads nothing"), grist.Authorize(grist.Authenticated))
	key := "shop/charge/t1/1"
	steps := []qtrace.Step{
		{Action: "bankPay", Actor: "shop/t1", Outcome: "ok", Args: map[string]any{"key": key}},
		{Action: "refund", Actor: "bank/t1", Outcome: "ok", Args: map[string]any{"id": key}},
	}
	for _, declared := range []bool{false, true} {
		if declared {
			grist.NewPeerCall(shop.Dependencies()[0], other)
		}
		problems := strings.Join(gristtest.Links(shop, bank, steps), "\n")
		if !strings.Contains(problems, "an answer the peer's commit does not give") || !strings.Contains(problems, "never called") {
			t.Errorf("refund declared %v: %s; want pay's missing answer and refund's uncalled command", declared, problems)
		}
	}
	// Unknown and pending callers make no promise about an answer, but a
	// peer's commit still has to be of the command that was sent.
	for _, outcome := range []string{"unknown", ""} {
		steps[0].Outcome = outcome
		steps[0].Invocation = outcome == ""
		if problems := strings.Join(gristtest.Links(shop, bank, steps), "\n"); !strings.Contains(problems, "never called") {
			t.Errorf("%s caller: %s; want refund's uncalled command", outcome, problems)
		}
	}
	// The command belongs in the match, but must not partition the
	// peer's idempotency namespace: two commands cannot commit one id.
	steps[0].Invocation, steps[0].Outcome = false, "ok"
	steps = append(steps,
		qtrace.Step{Action: "bankRefund", Actor: "shop/t1", Outcome: "ok", Args: map[string]any{"key": key}},
		qtrace.Step{Action: "pay", Actor: "bank/t1", Outcome: "ok", Args: map[string]any{"id": key}})
	if problems := strings.Join(gristtest.Links(shop, bank, steps), "\n"); !strings.Contains(problems, "a key names one outcome") {
		t.Errorf("two commands committed one id: %s; want a duplicate outcome", problems)
	}
}
