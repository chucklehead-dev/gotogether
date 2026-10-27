package counters

import (
	"context"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// mutant is the mutant this test binary was built with: set by the file
// a mutant's overlay adds (TestMutants), "" for the app as it is.
var mutant string

// setup gives every run a mailer of its own, and the store (for the
// dispatchDirect mutant's raw append).
func setup(s *qenv.Sim, st grist.Store) func(context.Context) context.Context {
	m := NewSimMailer(s)
	return func(ctx context.Context) context.Context { return WithDeps(ctx, Deps{Mailer: m, Store: st}) }
}

// The bindings: the commit side's, its test-only variant judging one
// welcome per counter, and the client side's.
const (
	commits = "counters.binding.yaml"
	once    = "counters.once.binding.yaml"
	calls   = "counters.calls.binding.yaml"
)

// system is the app under cfg, judged by binding, with faulty rates.
func system(binding string, cfg gristsim.Config, opts ...qtest.Option) qtest.System {
	cfg.Setup = setup
	return qtest.System{Binding: binding, Rates: faulty, Opts: opts, Mutant: mutant,
		Run: func(c qenv.Chooser) error { return gristsim.Run(c, App, cfg) }}
}

// faulty crashes the node (2 in 100 scheduling steps) and makes every
// other fault at the default rate: requests and answers lost, duplicated
// and late, commits whose answer is lost (with CommitFaults), the
// welcome's crash points.
var faulty = qenv.DefaultRates.Set("nemesis", 0.02)

// seeds is n, or short under -short.
func seeds(n, short int) int {
	if testing.Short() {
		return short
	}
	return n
}

// contended is claim 2's shape: one tenant and names from two letters,
// so that creates and increments collide.
var contended = gristsim.Config{Tenants: []string{"t1"}, Alphabet: []string{"a", "b"}, CommitFaults: true, Check: true}

// Claim 2: the safe default passes, through every fault. At Serializable
// and ReadCommitted, with commit faults and node crashes, and the
// scheduler's and the Store's own checks on, every run conforms and
// settles, and the faults happened. Snapshot is left out: every append
// writes the head, so its runs are Serializable's, byte for byte (the
// verdicts are cached across the two).
func TestFaithful(t *testing.T) {
	t.Parallel() // its explorations record into their own runs
	qtest.RequireQuint(t)
	for _, iso := range []qenv.Isolation{qenv.Serializable, qenv.ReadCommitted} {
		t.Run(iso.String(), func(t *testing.T) {
			cfg := contended
			cfg.Isolation = iso
			system(commits, cfg).
				Explore(t, qtest.ExploreConfig{Seeds: seeds(20, 4)}).
				Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()),
					qtest.Reaches("store.counters.commit").Faulted(), qtest.Reaches("nemesis.crash").Faulted())
		})
	}
}

// Claim 1's contrast: the faithful app at claim 1's configuration (two
// clients creating in one tenant, no faults) never commits a create the
// model refuses, where the blind mutant does in most runs. With grist's
// condition taken out of the runtime (the append unconditioned), this
// claim fails as the blind mutant's holds: 13 of 20 runs (6 seeds miss
// it with chance 0.2%).
func TestFaithfulAtClaim1(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	system(commits, gristsim.Config{Isolation: qenv.ReadCommitted, Tenants: []string{"t1"}, Send: []string{"create-counter"}}).
		WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: seeds(40, 6)}).
		Expect(t, qtest.None(qtest.Rejects("createCounter")), qtest.Conforms())
}

// A key's first answer is final, through duplicated and late requests:
// with the API duplicating and delaying three requests in ten, a refused
// command's late duplicate does not commit after its client was told
// no, nor a committed one's commit again. Both sides conform: the
// commits (every committed outcome needs its key fresh) and the clients,
// linearized. Without the marker (no key remembered) the duplicates
// commit twice: 19 of 20 runs fail at the commits; without refusal
// markers 18 of 20 (4 seeds miss either with chance under 0.1%).
func TestDuplicateRequests(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	rates := qenv.NoFaults.Set("rpc.api.delay", 0.3).Set("rpc.api.request", 0.3)
	cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 3, Ops: 4, Alphabet: []string{"a", "b"},
		Send: []string{"create-counter", "increment-counter", "show-counter"}}
	system(commits, cfg).WithRates(rates).Explore(t, qtest.ExploreConfig{Seeds: seeds(20, 4)}).
		Expect(t, qtest.Conforms(), qtest.Reaches("rpc.api.request").Faulted())
	system(calls, cfg, qtest.Linearize()).WithRates(rates).Explore(t, qtest.ExploreConfig{Seeds: seeds(40, 4)}).
		Expect(t, qtest.Conforms(), qtest.Reaches("rpc.api.request").Faulted())
}

// Claim 2, at the clients: the same runs recorded where the clients see
// them, linearized against the client-side model: a duplicate a retry
// created would show as a later read's mismatch.
func TestFaithfulAtClients(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	cfg := contended
	system(calls, cfg, qtest.Linearize()).
		Explore(t, qtest.ExploreConfig{Seeds: seeds(20, 4)}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("store.counters.commit").Faulted())
}

// crashAfterWelcome is a script of one fault: the node crashes after the
// welcome's send and before its checkpoint, the rest of each run its
// seed's.
func crashAfterWelcome(seed uint64) qenv.Chooser {
	base := qenv.NewSeeded(seed)
	base.Rates = qenv.NoFaults
	return &qenv.Script{Base: base, Faults: []qenv.Scripted{{Kind: "grist.welcome.effect", Name: "crashAfter"}}}
}

// okThenFailed is crashAfterWelcome, then three lost sends after it.
func okThenFailed(seed uint64) qenv.Chooser {
	base := qenv.NewSeeded(seed)
	base.Rates = qenv.NoFaults
	return &qenv.Script{Base: base, Faults: []qenv.Scripted{
		{Kind: "grist.welcome.effect", Name: "crashAfter"},
		{Kind: "rpc.mailer.request", Name: "drop", Follows: []int{0}},
		{Kind: "rpc.mailer.request", Name: "drop", Follows: []int{0}},
		{Kind: "rpc.mailer.request", Name: "drop", Follows: []int{0}},
	}}
}

// Claim 3: the duplicated welcome is the declared guarantee's, seen and
// judged. A crash after the send and before the checkpoint: the restarted
// node sends the welcome again under the same effect key. The model
// admits it (at least once); the test-only invariant welcomedOnce
// rejects exactly those runs. Exhaust within one fault reaches it too.
func TestWelcomeDuplicate(t *testing.T) {
	t.Parallel()
	qtest.RequireQuint(t)
	cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 3, Send: []string{"create-counter"}}
	n := seeds(10, 3)
	system(commits, cfg).Explore(t, qtest.ExploreConfig{Seeds: n, Chooser: crashAfterWelcome}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	system(once, cfg).Explore(t, qtest.ExploreConfig{Seeds: n, Chooser: crashAfterWelcome}).
		Expect(t, qtest.All(qtest.Violates("welcomedOnce")))
	// without the crash, no duplicate
	system(once, cfg).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: n}).
		Expect(t, qtest.None(qtest.Violates("welcomedOnce")))
	// an ok send, a crash before the checkpoint, then every try of the
	// restarted node's send lost: the committed run checkpoints
	// grist.failed, which at least once admits (atLeastOnceDone)
	system(commits, cfg).Explore(t, qtest.ExploreConfig{Seeds: n, Chooser: okThenFailed}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.Reaches("rpc.mailer.request").Faulted())
	if testing.Short() {
		return
	}
	one := gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 1}
	system(once, one).Exhaust(t, qtest.Bound{Faults: 1}).
		Expect(t, qtest.Some(qtest.Violates("welcomedOnce")))
	system(commits, one).Exhaust(t, qtest.Bound{Faults: 1}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
}
