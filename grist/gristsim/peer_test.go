package gristsim_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/grist/internal/peerkey"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

type (
	payArgs struct {
		Account string `json:"account"`
		Order   string `json:"order"`
	}
	ordered struct {
		Order   string `json:"order" grist:"tag"`
		Account string `json:"account"`
	}
	charged struct {
		Order string `json:"order" grist:"tag"`
	}
)

var errUnknownAccount = grist.Rejection("unknown-account")

// payPair is a pair of Apps: orders, whose charge processor pays each
// order at least once through its peer bank's pay (grist.NewPeerCall),
// and bank, which refuses an account it does not hold.
func payPair() (orders, bank *grist.App) {
	bank = grist.New("bank")
	paid := grist.NewEvent[payArgs](bank, "paid")
	pay := grist.NewCommand(bank, "pay", func(r *grist.Reads, c payArgs) ([]grist.Event, error) {
		if c.Account != "a" && c.Account != "b" {
			return nil, errUnknownAccount
		}
		return []grist.Event{paid.Of(c)}, nil
	}, grist.Unconditioned("a payment reads nothing"), grist.Refuses(errUnknownAccount), grist.Authorize(grist.Callers(grist.PeerCaller("orders"))))

	orders = grist.New("orders")
	placed := grist.NewEvent[ordered](orders, "ordered")
	done := grist.NewEvent[charged](orders, "charged")
	grist.NewCommand(orders, "order", func(r *grist.Reads, c ordered) ([]grist.Event, error) {
		return []grist.Event{placed.Of(c)}, nil
	}, grist.Unconditioned("an order reads nothing"), grist.Authorize(grist.Authenticated))
	payIt := grist.NewPeerCall(grist.NewPeer(orders, bank), pay)
	grist.NewProcessor(orders, "charge", placed, func(ctx context.Context, r *grist.Reads, e ordered, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			_, err := payIt.Do(ctx, payArgs{Account: e.Account, Order: e.Order})
			return err
		}, grist.AtLeastOnce, grist.OnSuccess(done.Of(charged{Order: e.Order})))
	}, grist.Delivers(grist.AtLeastOnce, done), grist.Uses(payIt))
	return orders, bank
}

// lossy are the pair's faults: the bank's answers lost (and its requests,
// late ones), each App's node crashing.
var lossy = qenv.NoFaults.Set("rpc.bank", 0.3).Set("nemesis", 0.01)

func pairRun(t *testing.T, seed uint64, cfg gristsim.Config) []qtrace.Step {
	t.Helper()
	orders, bank := payPair()
	cfg.Peers = []*grist.App{bank}
	sink := &qobs.MemorySink{}
	base := qenv.NewSeeded(seed)
	base.Rates = lossy
	c := qenv.WithContext(base, qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink)))
	if err := gristsim.Run(c, orders, cfg); err != nil {
		t.Fatal(err)
	}
	return sink.Steps()
}

// A peer runs beside the App on its own store, node and API: the App's
// effects call its command through grist/client over qhttp, each try
// under the effect's key (<app>/<processor>/<tenant>/<position>@<event-id>) as its
// command id, as the caller service:<app>; each App records under its
// own actors, so the two tenants t1 are two instances; and through lost
// answers and both nodes' crashes the two sides agree (gristtest.Links),
// the retries of one key answered by the bank's marker, one payment per
// order.
func TestPeerPair(t *testing.T) {
	retried := 0
	for seed := uint64(1); seed <= 8; seed++ {
		steps := pairRun(t, seed, gristsim.Config{Check: true, Send: []string{"order"}, Gen: map[string]func(func(int) int) any{
			"order": func(pick func(int) int) any {
				return ordered{Order: []string{"o1", "o2", "o3"}[pick(3)], Account: []string{"a", "b", "z"}[pick(3)]}
			},
		}})
		orders, bank := payPair()
		if p := gristtest.Links(orders, bank, steps); len(p) > 0 {
			t.Errorf("seed %d: %s", seed, strings.Join(p, "\n"))
		}
		tries := map[string]int{}
		paid := map[string]int{}
		for _, s := range steps {
			switch {
			case s.Actor == "orders/t1" && s.Action == "bankPay" && s.Invocation:
				key, _ := s.Args["key"].(string)
				if !strings.HasPrefix(key, "orders/charge/t1/") || s.Process != "orders" {
					t.Errorf("seed %d: the call %+v: want the effect's key, at orders", seed, s)
				}
				tries[key]++
			case s.Actor == "bank/t1" && s.Action == "pay" && s.Outcome == "ok":
				if s.Args["caller"] != "service:orders" || s.Process != "bank" {
					t.Errorf("seed %d: the payment %+v: want caller service:orders, at bank", seed, s)
				}
				paid[s.Args["id"].(string)]++
			case strings.HasPrefix(s.Actor, "t1"):
				t.Errorf("seed %d: step %s of actor %s: want every actor to name its App", seed, s.Action, s.Actor)
			}
		}
		for k, n := range tries {
			if n > 1 {
				retried++
			}
			if paid[k] > 1 {
				t.Errorf("seed %d: key %s paid %d times: want the marker to answer its retries", seed, k, paid[k])
			}
		}
	}
	if retried == 0 {
		t.Error("no call was retried over 8 seeds: want lost answers retried under their key")
	}
}

// Under the generated mutant freshKey (gristtest.PeerMutants: each try
// under a new command id), a retry after a lost answer is a second
// payment at the bank, under an id naming orders that no call has: Links
// finds it in some run, where the faithful pair's runs have none
// (TestPeerPair).
func TestPeerFreshKeyUnlinked(t *testing.T) {
	peerkey.Fresh = true
	t.Cleanup(func() { peerkey.Fresh = false })
	found := 0
	for seed := uint64(1); seed <= 8; seed++ {
		steps := pairRun(t, seed, gristsim.Config{Send: []string{"order"}})
		orders, bank := payPair()
		for _, p := range gristtest.Links(orders, bank, steps) {
			if strings.Contains(p, "which orders never called with") {
				found++
			}
		}
	}
	if found == 0 {
		t.Error("freshKey: no run's commit at the bank unlinked over 8 seeds")
	}
}

// A peer the App calls must run (Config.Peers), one it does not call must
// not, and a peer is no dependency of handlers (no Doubles).
func TestPeerConfigErrors(t *testing.T) {
	orders, bank := payPair()
	var payDep *grist.Dependency
	for _, d := range orders.Dependencies() {
		payDep = d
	}
	for _, c := range []struct {
		cfg  gristsim.Config
		want string
	}{
		{gristsim.Config{}, "calls peer bank (grist.NewPeer), which the run does not run: add its App to Config.Peers"},
		{gristsim.Config{Peers: []*grist.App{bank, grist.New("other")}}, "Config.Peers has other, which orders does not call"},
		{gristsim.Config{Peers: []*grist.App{bank}, Doubles: func() gristsim.Doubles { return gristsim.Doubles{payDep: nil} }}, "Config.Doubles has handlers of bank, a peer"},
	} {
		err := gristsim.Run(qenv.NewSeeded(1), orders, c.cfg)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Run: %v, want %q", err, c.want)
		}
	}
}
