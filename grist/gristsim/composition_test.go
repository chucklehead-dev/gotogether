package gristsim_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/grist/internal/gen"
	"github.com/chucklehead-dev/gotogether/grist/internal/peerkey"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

type posting struct {
	Order  string `json:"order" grist:"tag"`
	Amount int    `json:"amount"`
}

// postingChain is orders -> bank -> ledger: two asynchronous, at-least-once
// hops, each under its own App's effect key. direct adds orders -> ledger,
// so the ledger is shared by two callers, not two copies of one service.
func postingChain(direct bool) (orders, bank, ledger *grist.App) {
	return postingChainMutant(direct, "")
}

// missing omits the second hop; premature posts directly from orders,
// without depending on a committed payment, then suppresses bank's post.
func postingChainMutant(direct bool, mutant string) (orders, bank, ledger *grist.App) {
	direct = direct || mutant == "premature"
	ledger = grist.New("ledger")
	posted := grist.NewEvent[posting](ledger, "posted")
	post := grist.NewCommand(ledger, "post", func(r *grist.Reads, c posting) ([]grist.Event, error) {
		return []grist.Event{posted.Of(c)}, nil
	}, grist.Unconditioned("a posting reads nothing"), grist.Appends(posted),
		grist.Authorize(grist.Callers(grist.PeerCaller("bank"), grist.PeerCaller("orders"))))
	bank = grist.New("bank")
	paid := grist.NewEvent[posting](bank, "paid")
	pay := grist.NewCommand(bank, "pay", func(r *grist.Reads, c posting) ([]grist.Event, error) {
		return []grist.Event{paid.Of(c)}, nil
	}, grist.Unconditioned("a payment reads nothing"), grist.Appends(paid), grist.Authorize(grist.Callers(grist.PeerCaller("orders"))))
	ledgerPost := grist.NewPeerCall(grist.NewPeer(bank, ledger), post)
	grist.NewProcessor(bank, "post", paid, func(ctx context.Context, r *grist.Reads, e posting, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error {
			if mutant == "missing" || mutant == "premature" {
				return nil
			}
			_, err := ledgerPost.Do(ctx, e)
			return err
		}, grist.AtLeastOnce)
	}, grist.Delivers(grist.AtLeastOnce), grist.Uses(ledgerPost))
	orders = grist.New("orders")
	ordered := grist.NewEvent[posting](orders, "ordered")
	grist.NewCommand(orders, "place", func(r *grist.Reads, c posting) ([]grist.Event, error) {
		return []grist.Event{ordered.Of(c)}, nil
	}, grist.Unconditioned("an order reads nothing"), grist.Appends(ordered), grist.Authorize(grist.Authenticated))
	bankPay := grist.NewPeerCall(grist.NewPeer(orders, bank), pay)
	grist.NewProcessor(orders, "charge", ordered, func(ctx context.Context, r *grist.Reads, e posting, m grist.Meta) grist.Result {
		return grist.Effect(func(ctx context.Context) error { _, err := bankPay.Do(ctx, e); return err }, grist.AtLeastOnce)
	}, grist.Delivers(grist.AtLeastOnce), grist.Uses(bankPay))
	if direct {
		archive := grist.NewPeerCall(grist.NewPeer(orders, ledger), post)
		grist.NewProcessor(orders, "archive", ordered, func(ctx context.Context, r *grist.Reads, e posting, m grist.Meta) grist.Result {
			return grist.Effect(func(ctx context.Context) error { _, err := archive.Do(ctx, e); return err }, grist.AtLeastOnce)
		}, grist.Delivers(grist.AtLeastOnce), grist.Uses(archive))
	}
	return
}

// chainBindings supplies the business rules separately from the Go
// handlers. Each service's generated model judges only its own actors.
func chainBindings(t *testing.T, apps ...*grist.App) []string {
	t.Helper()
	var bindings []string
	for i, a := range apps {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "model"), 0o755); err != nil {
			t.Fatal(err)
		}
		command, event := []string{"place", "pay", "post"}[i], []string{"ordered", "paid", "posted"}[i]
		rules := fmt.Sprintf(`module %[1]sRules {
  import grist.* from "./grist"
  import %[1]sEvents.* from "./%[1]s_gen"
  pure val STRS: Set[str] = Set("a", "b")
  pure val INTS: Set[int] = 1.to(2)
  pure def %[2]sOutcome(log: List[Event], c: %[3]sArgs): str = "ok"
  pure def %[2]sEvents(log: List[Event], c: %[3]sArgs): List[Event] = [%[4]s(c.order, c.amount)]
  pure def %[2]sNeeds(c: %[3]sArgs, result: str): Set[Read] = Set()
`, a.Name, command, strings.ToUpper(command[:1])+command[1:], event)
		if i < 2 {
			processor, call := []string{"charge", "post"}[i], []string{"BankPay", "LedgerPost"}[i]
			rules += fmt.Sprintf(`  pure def %[1]sSkips(prefix: List[Event], trigger: int): bool = false
  pure def %[1]s%[2]s(prefix: List[Event], trigger: int): %[2]sRequest = {
    order: %[3]sOrder(prefix[trigger - 1]), amount: %[3]sAmount(prefix[trigger - 1]),
  }
`, processor, call, event)
		}
		if i == 0 && len(a.Dependencies()) > 1 {
			rules += `  pure def archiveSkips(prefix: List[Event], trigger: int): bool = false
  pure def archiveLedgerPost(prefix: List[Event], trigger: int): LedgerPostRequest = {
    order: orderedOrder(prefix[trigger - 1]), amount: orderedAmount(prefix[trigger - 1]),
  }
`
		}
		rules += "}\n"
		model := fmt.Sprintf(`module %[1]s {
  import grist.* from "./grist"
  import %[1]sEvents.* from "./%[1]s_gen"
  import %[1]sRules.* from "./%[1]s_rules"
  import %[1]sGen.* from "./%[1]s_steps"
  export %[1]sGen.*
}
module %[1]sCalls {
  import grist.* from "./grist"
  import %[1]sEvents.* from "./%[1]s_gen"
  import %[1]sRules.* from "./%[1]s_rules"
  import %[1]sCallsGen.* from "./%[1]s_steps"
  export %[1]sCallsGen.*
}
`, a.Name)
		for name, src := range map[string]string{a.Name + ".qnt": model, a.Name + "_rules.qnt": rules} {
			if err := os.WriteFile(filepath.Join(dir, "model", name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		g, err := gen.Generate(a, dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Missing) > 0 {
			t.Fatalf("%s's model lacks %v", a.Name, g.Missing)
		}
		if err := g.Write(dir); err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, filepath.Join(dir, a.Name+".binding.yaml"))
	}
	return bindings
}

// postingBalance is the independent system property: each accepted order
// contributes one posting with the same tenant, order and amount. It reads
// committed commands, never what a caller says it sent or was told. Each
// service can conform locally while the composition duplicates a posting.
func postingBalance(steps []qtrace.Step, copies int) bool {
	type entry struct {
		tenant string
		order  any
		amount any
	}
	want, got := map[entry]int{}, map[entry]int{}
	for _, s := range steps {
		if s.Invocation || (s.Outcome != "" && s.Outcome != "ok") {
			continue
		}
		parts := strings.Split(s.Actor, "/")
		if len(parts) != 2 {
			continue
		}
		k := entry{parts[1], s.Args["order"], s.Args["amount"]}
		if parts[0] == "orders" && s.Action == "place" {
			want[k] += copies
		}
		if parts[0] == "ledger" && s.Action == "post" {
			got[k]++
		}
	}
	return len(want) > 0 && reflect.DeepEqual(want, got)
}

// The first GR4 experiment: three models judge the very same histories,
// both edges agree, and postings are conserved after lost answers on both
// hops and crashes of both processors after their effects. freshKey is the
// contrast: every local model still conforms, but the cross-service checks
// detect the extra payments/postings. It changes no model or declaration.
func TestPeerComposition(t *testing.T) {
	qtest.RequireQuint(t)
	orders, bank, ledger := postingChain(false)
	bindings := chainBindings(t, orders, bank, ledger)
	cfg := gristsim.Config{Tenants: []string{"t1", "t2"}, Clients: 1, Ops: 3, Check: true, CommitFaults: true,
		Send: []string{"place"}, Ints: []int{1, 2}, Alphabet: []string{"a", "b"}, Peers: []*grist.App{ledger, bank}}
	system := gristsim.System(bindings[0], orders, cfg).WithRates(qenv.NoFaults)
	lost := []qenv.Scripted{{Kind: "rpc.bank.response", Name: "lost"}, {Kind: "rpc.ledger.response", Name: "lost"}}
	for _, scenario := range []struct {
		name   string
		fresh  bool
		faults []qenv.Scripted
	}{
		{"lost answers", false, lost},
		{"crashes", false, []qenv.Scripted{{Kind: "grist.charge.effect", Name: "crashAfter"}, {Kind: "grist.post.effect", Name: "crashAfter"}}},
		{"lost commits", false, []qenv.Scripted{{Kind: "store.bank.commit", Name: "unknownOutcome"}, {Kind: "store.ledger.commit", Name: "unknownOutcome"}}},
		{"freshKey", true, lost},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// The generated mutant's switch is process-wide, so this test
			// and its subtests must not run in parallel.
			peerkey.Fresh = scenario.fresh
			t.Cleanup(func() { peerkey.Fresh = false })
			sys := system
			if scenario.fresh {
				sys.Mutant = peerkey.Mutant
			}
			e := sys.Explore(t, qtest.ExploreConfig{Seeds: 8, ShortSeeds: 2, Chooser: func(seed uint64) qenv.Chooser {
				base := qenv.NewSeeded(seed)
				base.Rates = qenv.NoFaults
				return &qenv.Script{Base: base, Faults: scenario.faults}
			}})
			claims := []qtest.Claim{qtest.Conforms(), qtest.All(qtest.Settles())}
			for _, f := range scenario.faults {
				claims = append(claims, qtest.Reaches(f.Kind).Faulted())
			}
			e.Expect(t, claims...)
			for _, r := range e.Runs {
				for _, binding := range bindings[1:] {
					qtest.Validate(t, binding, r.Recorded).Expect(t, qtest.Conforms(), qtest.Settles())
				}
				projected, err := gristtest.ProtocolSteps(r.Recorded, orders, bank, ledger)
				if err != nil {
					t.Fatal(err)
				}
				claim := qtest.Conforms()
				if scenario.fresh {
					claim = qtest.Rejects("bankPay")
				}
				qtest.Validate(t, "testdata/protocol/chain.binding.yaml", projected).Expect(t, claim)
				linked := len(gristtest.Links(orders, bank, r.Recorded)) == 0 && len(gristtest.Links(bank, ledger, r.Recorded)) == 0
				if linked == scenario.fresh || postingBalance(r.Recorded, 1) == scenario.fresh {
					t.Errorf("freshKey=%v: linked=%v, postings conserved=%v; want both %v", scenario.fresh, linked, postingBalance(r.Recorded, 1), !scenario.fresh)
				}
			}
		})
	}
}

// A diamond reaches the same ledger directly and through the bank. It
// must allocate one ledger and attach both callers to it, independently
// of Config.Peers' order, with both sets of command ids recorded there.
func TestPeerShared(t *testing.T) {
	orders, bank, ledger := postingChain(true)
	for _, peers := range [][]*grist.App{{bank, ledger}, {ledger, bank}} {
		base := qenv.NewSeeded(3)
		base.Rates = qenv.NoFaults
		sink := &qobs.MemorySink{}
		c := qenv.WithContext(base, qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink)))
		if err := gristsim.Run(c, orders, gristsim.Config{Peers: peers, Send: []string{"place"}, Clients: 1, Ops: 2, Check: true}); err != nil {
			t.Fatal(err)
		}
		steps := sink.Steps()
		for _, edge := range [][2]*grist.App{{orders, bank}, {orders, ledger}, {bank, ledger}} {
			gristtest.Linked(t, edge[0], edge[1], steps)
		}
		if !postingBalance(steps, 2) {
			t.Error("shared ledger: want one posting per order from each path")
		}
	}
}
