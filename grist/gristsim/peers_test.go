package gristsim

import (
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
)

// Invalid topologies fail before any qenv endpoint is allocated. In
// particular equal names may not merge Apps or fault kinds silently.
func TestPeerGraphErrors(t *testing.T) {
	for _, name := range []string{"missing direct", "missing indirect", "unreachable", "duplicate", "nil", "root", "same name", "wrong App", "root name", "cycle", "root cycle", "API collision", "double collision", "process collision", "cluster"} {
		t.Run(name, func(t *testing.T) {
			root, bank, ledger := grist.New("orders"), grist.New("bank"), grist.New("ledger")
			grist.NewPeer(root, bank)
			cfg := defaults(Config{Peers: []*grist.App{bank}})
			var want string
			switch name {
			case "missing direct":
				cfg.Peers = nil
				want = "orders calls peer bank"
			case "missing indirect":
				grist.NewPeer(bank, ledger)
				want = "bank calls peer ledger"
			case "unreachable":
				cfg.Peers = append(cfg.Peers, ledger)
				want = "ledger, which orders does not call"
			case "duplicate":
				cfg.Peers = append(cfg.Peers, bank)
				want = "has bank twice"
			case "nil":
				cfg.Peers = append(cfg.Peers, nil)
				want = "holds nil or the App itself"
			case "root":
				cfg.Peers = append(cfg.Peers, root)
				want = "holds nil or the App itself"
			case "same name":
				cfg.Peers = append(cfg.Peers, grist.New("bank"))
				want = "two Apps named bank"
			case "wrong App":
				cfg.Peers = []*grist.App{grist.New("bank")}
				want = "Config.Peers holds another App of that name"
			case "root name":
				cfg.Peers = append(cfg.Peers, grist.New("orders"))
				want = "two Apps named orders"
			case "cycle":
				grist.NewPeer(bank, ledger)
				grist.NewPeer(ledger, bank)
				cfg.Peers = append(cfg.Peers, ledger)
				want = "cycle orders -> bank -> ledger -> bank"
			case "root cycle":
				grist.NewPeer(bank, root)
				want = "cycle orders -> bank -> orders"
			case "API collision":
				grist.NewDependency(bank, "orders")
				want = "App orders and dependency orders of bank share rpc.orders.*"
			case "double collision":
				grist.NewDependency(root, "mailer")
				grist.NewDependency(bank, "mailer")
				want = "dependency mailer of orders and dependency mailer of bank share rpc.mailer.*"
			case "process collision":
				node := grist.New("node2")
				grist.NewPeer(bank, node)
				cfg.Peers = append(cfg.Peers, node)
				cfg.Nodes = 2
				want = "peer node2 has the name of the root's node process"
			case "cluster":
				cfg.Cluster = &Cluster{}
				want = "Config.Peers with Config.From or Config.Cluster"
			}
			if _, err := peerGraph(root, cfg); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("peerGraph: %v; want %q", err, want)
			}
		})
	}
}

// A shared peer is visited once, in declaration order rather than the
// caller-supplied list's order: swapping the list cannot change a seed.
func TestPeerGraphOrder(t *testing.T) {
	root, bank, ledger := grist.New("orders"), grist.New("bank"), grist.New("ledger")
	grist.NewPeer(root, bank)
	grist.NewPeer(root, ledger)
	grist.NewPeer(bank, ledger)
	for _, peers := range [][]*grist.App{{bank, ledger}, {ledger, bank}} {
		got, err := peerGraph(root, defaults(Config{Peers: peers}))
		if err != nil || !reflect.DeepEqual(got, []*grist.App{bank, ledger}) {
			t.Fatalf("peerGraph: %v, %v; want bank, ledger once each", got, err)
		}
	}
}
