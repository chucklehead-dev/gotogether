package raftkv

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
	"go.etcd.io/raft/v3/tracker"
)

// system is the cluster under cfg as one System, checked against the
// binding of what cfg records: the clients' history linearized whole, or
// the cluster's leadership in the order recorded.
func system(cfg Config, opts ...qtest.Option) qtest.System {
	if opts == nil {
		opts = []qtest.Option{qtest.Linearize()}
	}
	s := qtest.System{Binding: "raftkv.binding.yaml", Rates: faulty, Opts: append(opts, qtest.LinearizeBudget(5000)),
		Run: func(c qenv.Chooser) error { return Run(c, cfg) }}
	if cfg.Record == Cluster {
		s.Binding, s.Opts = "cluster.binding.yaml", nil
	}
	if cfg.Mutation != Faithful {
		s.Mutant = cfg.Mutation.String()
	}
	return s
}

// faulty loses, duplicates and delays one message in twenty, and crashes
// a node about once a second (a crash is considered every 50ms of
// virtual time).
var faulty = qenv.DefaultRates.Set("rpc", 0.05).Set("nemesis.crash", 0.05)

// Through lost, late and duplicated messages and crashed nodes, the
// clients' history is a linearizable store's, every call answers, the
// system settles and every node converges.
func TestFaithful(t *testing.T) {
	qtest.RequireQuint(t)
	system(Config{}).Explore(t, qtest.ExploreConfig{Seeds: 10}).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.All(qtest.Completes()))
}

// A snapshot lost on the way (a call that failed, or one delivered to a
// node that crashed before it stepped it) leaves raft waiting for it
// forever: the leader pauses the peer until it is told the snapshot
// failed. The first version never told it, and 2 of these 40 runs never
// converged (a review found it); the node reports failed sends and
// retries a snapshot left unacknowledged.
func TestLostSnapshots(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("explores 40 runs with compaction under one message fault in ten")
	}
	system(Config{Ops: 12}).WithRates(faulty.Set("rpc", 0.1)).Explore(t, qtest.ExploreConfig{Seeds: 40}).
		Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.All(qtest.Completes()))
}

// Through the same faults, no two nodes lead one term, and the settled
// cluster has a leader.
func TestClusterFaithful(t *testing.T) {
	qtest.RequireQuint(t)
	system(Config{Record: Cluster}).Explore(t, qtest.ExploreConfig{Seeds: 10}).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
}

// A client that gives up after five attempts leaves its call pending: its
// effect is unknown (a transfer whose answer was lost took effect).
// Linearize alone places no pending call, and the reads and the probe
// after the settle contradict an order without it; with ResolvePending
// placing each (or none) every run conforms. A client that retries under
// a new request id (NoRequestID) applies a transfer twice, which no
// placement of one call explains.
func TestPendingTransfers(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("explores runs linearized with their pending calls placed")
	}
	cfg := Config{MaxAttempts: 5}
	lossy := faulty.Set("rpc", 0.1)
	broken := qtest.Some(qtest.AnyOf(qtest.Mismatch("read"), qtest.Mismatch("cas"), qtest.Mismatch("transfer"), qtest.Violates(validate.EventuallyName("converged"))))
	both := []qtest.Option{qtest.Linearize(), qtest.ResolvePending()}
	x := qtest.ExploreConfig{Seeds: 12}
	system(cfg, both...).WithRates(lossy).Explore(t, x).Expect(t, qtest.Conforms(), qtest.Some(qtest.Pending("transfer")))
	// 3 runs in 12
	system(cfg).WithRates(lossy).Explore(t, x).Expect(t, broken)
	cfg.Mutation = NoRequestID
	system(cfg, both...).WithRates(lossy).Explore(t, x).Expect(t, broken)
}

// partitioned cuts every node off from the others for 2.5s once a leader
// is elected, then heals the network: the cut leader steps down (a
// leader that hears from no quorum for an election timeout does, at most
// twice it later), and an election follows the heal.
func partitioned(ctx context.Context, d *Deployment) {
	qenv.Wait(ctx, func() bool { return d.Leader() >= 0 })
	qenv.Sleep(ctx, 100*time.Millisecond)
	for i := range d.Nodes {
		d.Isolate(ctx, i)
	}
	qenv.Sleep(ctx, 2500*time.Millisecond)
	d.Heal(ctx)
}

// After the network heals, a node leads within the bound (300ms): a
// follower cut off goes on pre-campaigning every election timeout (100
// to 200ms), and wins once the network is whole. An election timeout ten
// times as long (SlowElection) still elects, so the cluster has a leader
// when it settles, but too late for the bound: only the bounded
// obligation tells them apart.
func TestElectionAfterHeal(t *testing.T) {
	qtest.RequireQuint(t)
	// the run outlasts the scenario: a settle heals the network unrecorded
	cfg := Config{Record: Cluster, Scenario: partitioned, Duration: 6 * time.Second}
	x := qtest.ExploreConfig{Seeds: 10}
	system(cfg).WithRates(qenv.NoFaults).Explore(t, x).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	cfg.Mutation = SlowElection
	system(cfg).WithRates(qenv.NoFaults).Explore(t, x).
		Expect(t, qtest.None(qtest.Violates(validate.EventuallyName("hasLeader"))), qtest.Some(qtest.Violates("within 300ms after healed: hasLeader")))
}

// isolateFirst moves the leadership to n1 once some writes are done, as an
// operator does before maintenance, then cuts n1 off for 400ms: the
// others elect a new leader, which takes the writes, while n1 believes it
// leads until its quorum check. (The leader an election makes is the node
// whose timer fires first: under drift, the fastest clock. The transfer
// is how a slow one comes to lead.)
func isolateFirst(ctx context.Context, d *Deployment) {
	qenv.Sleep(ctx, 200*time.Millisecond)
	qenv.Wait(ctx, func() bool { return d.Leader() >= 0 })
	d.TransferLeader(ctx, 0)
	qenv.Sleep(ctx, 100*time.Millisecond)
	d.Isolate(ctx, 0)
	qenv.Sleep(ctx, 400*time.Millisecond)
	d.Heal(ctx)
}

// A leader that answers reads on its lease (ReadOnlyLeaseBased) answers
// them while cut off, after the others elected a new leader that took
// writes: a stale read. The lease holds only while clocks agree: raft's
// pre-vote makes a follower refuse to elect while it is in the leader's
// lease, and the leader steps down when its quorum check fails, each
// timed on its own clock. A slow leader's check comes late, fast
// followers' leases end early, and between them two nodes lead. Every
// combination of the three clocks' drifts (none, fast, slow) is run, 27:
// 3 read stale. The faithful leader confirms it still leads with a quorum
// before it reads (ReadOnlySafe), whatever the clocks.
func TestLeaseRead(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("explores every combination of three clocks' drifts, twice")
	}
	cfg := Config{Readers: true, Ops: 10, Scenario: isolateFirst, Clocks: qenv.Clocks{Drift: 0.8}}
	every := qtest.Bound{Faults: 3, Without: []string{"rpc", "nemesis"}}
	drifting := qenv.NoFaults.Set("clock.drift", 1)
	system(cfg).WithRates(drifting).Exhaust(t, every).Expect(t, qtest.Conforms(), qtest.All(qtest.Completes()))
	cfg.Mutation = LeaseRead
	system(cfg).WithRates(drifting).Exhaust(t, every).Expect(t, qtest.Some(qtest.Mismatch("read")))
}

// A rolling upgrade to v2 while the clients write, one node every 500ms:
// the cluster writes v1 until every node runs v2 and it committed the
// switch, so a node not yet upgraded reads every entry, and the cluster
// ends on the v2 log. A rollout paused after two nodes (the third still
// on v1) conforms too. v2 code that writes its format at once (EagerV2)
// leads in the mixed cluster (an upgrade restarts the leader, and an
// upgraded node may win the election), and the v1 node skips its
// entries: its state machine misses writes, which the probe after the
// settle shows. A full rollout hides it: the last node's upgrade replays
// its log with v2 code, unless it compacted the skipped entries away
// first.
func TestRollingUpgrade(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("explores rollouts, the mutant's failing runs proved by search")
	}
	cfg := Config{Rollout: 3, Ops: 20, Think: 80 * time.Millisecond}
	x := qtest.ExploreConfig{Seeds: 10}
	system(cfg).WithRates(qenv.NoFaults).Explore(t, x).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()), qtest.All(qtest.Completes()))
	d, err := RunDeployment(qenv.NewSeeded(1), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if v := d.Versions(); strings.Count(v, "code v2 log v2") != 3 {
		t.Errorf("after the rollout: %s, want every node on v2 code and the v2 log", v)
	}
	paused := Config{Rollout: 2, Ops: 20, Think: 80 * time.Millisecond}
	system(paused).WithRates(qenv.NoFaults).Explore(t, x).Expect(t, qtest.Conforms())
	// 8 runs in 20
	paused.Mutation = EagerV2
	system(paused).WithRates(qenv.NoFaults).Explore(t, qtest.ExploreConfig{Seeds: 20}).
		Expect(t, qtest.Some(qtest.AnyOf(qtest.Mismatch("read"), qtest.Violates(validate.EventuallyName("converged")))))
}

// elections deposes the leader every 300ms (cut off for 150ms, then
// healed), so a run holds many elections, each a chance for two
// candidates in one term.
func elections(ctx context.Context, d *Deployment) {
	for range 6 {
		qenv.Sleep(ctx, 150*time.Millisecond)
		qenv.Wait(ctx, func() bool { return d.Leader() >= 0 })
		d.Isolate(ctx, d.Leader())
		qenv.Sleep(ctx, 150*time.Millisecond)
		d.Heal(ctx)
	}
}

// A vote raft does not find in the log after a restart can be cast again:
// a node votes for one candidate, crashes and restarts, and votes for
// another of the same term, which then leads it beside the first. It
// takes two candidates in one term, a crash between the votes and the
// second's request arriving after the restart (a late message). The
// faithful node persists its vote before it answers. (So many faults slow
// an election past the bound after a heal, which this test does not
// claim: it is about a healthy network.)
func TestUnpersistedVote(t *testing.T) {
	qtest.RequireQuint(t)
	cfg := Config{Record: Cluster, Scenario: elections, Duration: 3 * time.Second}
	rates := qenv.NoFaults.Set("nemesis.crash", 0.3).Set("rpc", 0.2)
	x := qtest.ExploreConfig{Seeds: 30}
	system(cfg).WithRates(rates).Explore(t, x).
		Expect(t, qtest.None(qtest.Violates("electionSafety")), qtest.None(qtest.AnyOf(qtest.Rejects("lead"), qtest.Rejects("stepDown"))))
	// 5 runs in 50 (4 in 30 at first: 49 seeds miss it one time in a thousand)
	cfg.Mutation = NoPersistVote
	system(cfg).WithRates(rates).Explore(t, qtest.ExploreConfig{Seeds: 50}).Expect(t, qtest.Some(qtest.Violates("electionSafety")))
}

// Two hundred operations (four clients, fifty each), every run's history
// linearized whole: a transfer touches two keys, so no partition by key
// helps, and the search orders the whole history.
func TestLinearizeScale(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("linearizes five histories of 200 operations")
	}
	sys := system(Config{Clients: 4, Ops: 50, Duration: 10 * time.Second})
	sys.Opts = append(sys.Opts, qtest.LinearizeBudget(50000))
	sys.Explore(t, qtest.ExploreConfig{Seeds: 5}).Expect(t, qtest.Conforms(), qtest.All(qtest.Completes()))
}

// downWhileWriting crashes a follower and keeps it down while the clients
// write, long enough for the leader to compact past it, then restarts
// it: it can catch up only by a snapshot, which the scenario loses. When
// the leader starts sending it (the follower's progress in
// StateSnapshot), the link between them is cut for 200ms: the call
// carrying the snapshot fails.
func downWhileWriting(ctx context.Context, d *Deployment) {
	qenv.Wait(ctx, func() bool { return d.Leader() >= 0 })
	l := d.Leader()
	fi := (l + 1) % len(d.Nodes)
	f := d.Nodes[fi].Proc
	if !f.Down() {
		f.Crash()
	}
	qenv.Sleep(ctx, 1500*time.Millisecond)
	if !f.Down() {
		return
	}
	f.Restart()
	snapshotting := func() bool {
		inc := d.Nodes[l].inc
		if inc == nil || d.Leader() != l {
			return false
		}
		pr, ok := inc.rn.Status().Progress[uint64(fi+1)]
		return ok && pr.State == tracker.StateSnapshot
	}
	if !qenv.WaitFor(ctx, snapshotting, time.Second) {
		return // the leader changed, or caught it up by its log
	}
	d.Sim.Net().Partition(d.Nodes[l].Proc, f)
	qenv.Sleep(ctx, 200*time.Millisecond)
	d.Sim.Net().Heal(d.Nodes[l].Proc, f)
}

// A node that restarts behind catches up within a second (cluster.qnt's
// caughtUp, the binding's within): by a snapshot, the leader having
// compacted past it, which the scenario loses on the way. Never told a
// snapshot failed (NoSnapshotReport), raft waits for it forever and the
// node stays behind; the cluster still settles with a leader, so only
// the bound tells them apart.
func TestCatchUp(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("explores 40 runs with compaction")
	}
	name := "within 1s after behind: caughtUp"
	cfg := Config{Record: Cluster, Load: true, Ops: 20, Scenario: downWhileWriting}
	system(cfg).WithRates(faulty.Set("rpc", 0.1).Set("nemesis.crash", 0)).Explore(t, qtest.ExploreConfig{Seeds: 20}).
		Expect(t, qtest.Conforms())
	cfg.Mutation = NoSnapshotReport
	system(cfg).WithRates(faulty.Set("rpc", 0.1).Set("nemesis.crash", 0)).Explore(t, qtest.ExploreConfig{Seeds: 20}).
		Expect(t, qtest.Some(qtest.Violates(name)))
}
