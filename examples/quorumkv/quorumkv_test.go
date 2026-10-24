package quorumkv

import (
	"fmt"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// system is the cluster under cfg as one System, its history checked
// linearized key by key.
func system(cfg Config) qtest.System {
	s := qtest.System{Binding: "quorumkv.binding.yaml", Rates: faulty, Opts: []qtest.Option{qtest.LinearizeByKey("key")},
		Run: func(c qenv.Chooser) error { return Run(c, cfg) }}
	if cfg.Mutation != Faithful {
		s.Mutant = fmt.Sprint(cfg.Mutation)
	}
	return s
}

// faulty loses, duplicates and delays one request in ten and crashes
// replicas.
var faulty = qenv.DefaultRates.Set("rpc", 0.1)

// Through lost, late and duplicated messages and crashed replicas, every
// key's history is a register's, every call answers, the system settles
// and every replica converges.
func TestFaithful(t *testing.T) {
	qtest.RequireQuint(t)
	system(Config{}).Explore(t, qtest.ExploreConfig{Seeds: 10}).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.All(qtest.Completes()))
}

// With one replica down for good, a quorum of the other two finishes
// every call; waiting for all three (W3) never does, and the settle around
// the two says so. Plain Settle would restart nothing either, but faults
// would stop: SettleCore keeps them going outside the core.
func TestQuorumWithOneDown(t *testing.T) {
	qtest.RequireQuint(t)
	core := Config{Core: []string{"r1", "r2"}}
	system(core).Explore(t, qtest.ExploreConfig{Seeds: 5}).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.All(qtest.Completes()))
	core.Mutation = W3
	system(core).Explore(t, qtest.ExploreConfig{Seeds: 5}).Expect(t, qtest.None(qtest.Settles()))
}

// A put given up after one failed round leaves its call pending: no step
// says what it did, and Completes fails of the run. The faithful client
// retries until a quorum acknowledges (TestFaithful: every call completes).
func TestLeakedPut(t *testing.T) {
	qtest.RequireQuint(t)
	system(Config{Mutation: Leak}).Explore(t, qtest.ExploreConfig{Seeds: 10}).Expect(t, qtest.Some(qtest.Pending("write")))
}

// Without hinted handoff a replica that missed a write keeps the old value:
// every read conforms (a quorum read masks it), and only the probe after
// the settle shows the replicas never converged.
func TestNoHandoff(t *testing.T) {
	qtest.RequireQuint(t)
	// each client ends on a write: a read after it would write back to the
	// replica the write missed, and repair what the mutant broke; in
	// about one run in ten (2 of 20), which 70 seeds miss about once in
	// a thousand
	system(Config{Mutation: NoHandoff, Ops: 3}).WithRates(qenv.DefaultRates.Set("rpc", 0.3)).Explore(t, qtest.ExploreConfig{Seeds: 70}).
		Expect(t, qtest.Some(qtest.Violates(validate.EventuallyName("converged"))), qtest.None(qtest.Mismatch("read")))
}

// pct schedules each seed's run by PCT at depth 2, faults at sys's rates.
func pct(sys qtest.System) func(seed uint64) qenv.Chooser {
	return func(seed uint64) qenv.Chooser {
		base := qenv.NewSeeded(seed)
		base.Rates = sys.FaultRates()
		return qenv.NewPCT(base, seed, 2, 400)
	}
}

// oneKey is three clients on one key: operations of one key overlap,
// which the mutants below need.
var oneKey = Config{Clients: 3, Ops: 4, Keys: []string{"a"}}

// contended is cfg's system at a fifth of messages faulted, its key's
// history linearized within 1000 orders: a contended key's history takes
// more than the default budget to rule out every order.
func contended(cfg Config) qtest.System {
	sys := system(cfg).WithRates(qenv.DefaultRates.Set("rpc", 0.2))
	sys.Opts = append(sys.Opts, qtest.LinearizeBudget(1000))
	return sys
}

// A read that answers without writing back can see a write that a later
// read does not, when the write reached one replica and stalls: a stale
// read no order of the key's history explains. It takes a reader between
// a writer's two replicas, which PCT's priorities make: uniform seeds
// found none in 10 runs, PCT 1 in 10 (4 in 20 since: 35 seeds miss it
// about once in a thousand).
func TestBlindRepair(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("explores 35 runs of a contended key, each linearized")
	}
	cfg := Config{Mutation: BlindRepair, Clients: 3, Ops: 6, Keys: []string{"a"}, Writers: 1}
	sys := contended(cfg)
	sys.Explore(t, qtest.ExploreConfig{Seeds: 35, Chooser: pct(sys)}).Expect(t, qtest.Some(qtest.Mismatch("read")))
}

// A retried put that picks a new version writes its value twice, around a
// later write: readers see it, then the other, then it again. The first
// faithful version of this store did it.
func TestRepick(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("explores 20 runs of a contended key, each linearized")
	}
	cfg := oneKey
	cfg.Mutation = Repick
	contended(cfg).Explore(t, qtest.ExploreConfig{Seeds: 20}).Expect(t, qtest.Some(qtest.Mismatch("read")))
}
