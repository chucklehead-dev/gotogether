// Package raftkv is a replicated key-value store on etcd's raft library
// (go.etcd.io/raft/v3), the library itself, unchanged, run in qenv: three
// nodes, each a crashable process with a write-ahead log on a Store,
// exchanging raft's messages over a lossy network, serving clients from
// the leader. Raft's own election randomness is drawn from the simulation
// (entropy.go), so every run replays.
//
// A run records one of two histories (Config.Record), each checked by its
// own model:
//
//   - KV, the clients' (model/raftkv.qnt): puts, gets, compare-and-sets
//     and transfers between keys, each a call with its interval
//     (qobs.Invoke), checked linearized (a transfer touches two keys, so
//     the history is one register's, not one per key); a client that gives
//     up leaves its call pending; after the settle, what every node holds.
//   - Cluster, the nodes' leadership (model/cluster.qnt): campaigns,
//     terms led and stepped down from, crashes, and the partitions the
//     test makes: at most one leader a term, and a leader within a bound
//     of the network healing.
//
// The mutants each break one thing a real deployment gets wrong:
//
//   - NoRequestID: a client's retry is a new request, so a transfer whose
//     answer was lost applies twice.
//   - LeaseRead: the leader answers reads on its lease
//     (ReadOnlyLeaseBased) instead of confirming it still leads; a clock
//     that runs slow lets a deposed leader answer stale.
//   - SlowElection: an election timeout ten times as long, which still
//     elects, too late for the bound after a heal.
//   - NoPersistVote: the vote is not written to the log, so a node that
//     restarts can vote twice in a term.
//   - EagerV2: the upgraded code writes its new log format at once, which
//     the nodes not yet upgraded skip.
package raftkv

import (
	"context"
	"fmt"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"go.etcd.io/raft/v3"
)

// Mutation injects a bug.
type Mutation int

const (
	Faithful Mutation = iota
	// NoRequestID gives a client's retry a new request id.
	NoRequestID
	// LeaseRead serves reads on the leader's lease.
	LeaseRead
	// SlowElection waits ten times as long before campaigning.
	SlowElection
	// NoPersistVote writes the hard state without its vote.
	NoPersistVote
	// EagerV2 writes the v2 log format as soon as a node runs v2.
	EagerV2
)

// Mutations names each mutation, for run-demo.sh.
var Mutations = map[string]Mutation{"": Faithful, "no-request-id": NoRequestID, "lease-read": LeaseRead,
	"slow-election": SlowElection, "no-persist-vote": NoPersistVote, "eager-v2": EagerV2}

func (m Mutation) String() string {
	for k, v := range Mutations {
		if v == m && k != "" {
			return k
		}
	}
	return "faithful"
}

// Record is the history a run records.
type Record int

const (
	// KV records the clients' calls and the probe after the settle.
	KV Record = iota
	// Cluster records the nodes' leadership and the test's partitions.
	Cluster
)

// Nodes are the nodes' names, the models' NODES.
var Nodes = []string{"n1", "n2", "n3"}

// Keys are the keys, the model's KEYS; each starts at Initial.
var Keys = []string{"a", "b", "c"}

// Initial is every key's value when the cluster is created.
const Initial = 10

// nemesisEvery is how often a crash is considered, in virtual time: at
// every scheduling step raft's ticks would crash a node every few
// milliseconds.
var nemesisEvery = 50 * time.Millisecond // read-only after init

// Config is a run's shape.
type Config struct {
	Mutation Mutation
	Record   Record
	Clients  int // 3 by default
	Ops      int // per client, 6 by default
	// Think is how long a client waits between its operations (none by
	// default): a client that runs as long as a rollout.
	Think time.Duration
	// MaxAttempts is how many times a client tries an operation before it
	// gives up, leaving its call pending; 50 by default.
	MaxAttempts int
	// Duration is how long the clients run before the settle; 2s by
	// default.
	Duration time.Duration
	// Rollout upgrades the first Rollout nodes to v2, one every 500ms,
	// while the clients run, as an operator rolls a deployment (3: all of
	// them; fewer: a rollout paused, the others still on v1).
	Rollout int
	// Scenario, if set, runs as a task alongside the clients: a test's
	// partitions, recorded (Partition, Heal).
	Scenario func(ctx context.Context, d *Deployment)
	// Partitions turns on the network's random partitions (not recorded:
	// KV runs only).
	Partitions bool
	// Clocks are the nodes' clock faults.
	Clocks qenv.Clocks
	// Readers makes the clients a reader per node, asking it first, reads
	// of one key only, and one writer of it.
	Readers bool
}

func (cfg *Config) defaults() {
	if cfg.Clients == 0 {
		cfg.Clients = 3
	}
	if cfg.Ops == 0 {
		cfg.Ops = 6
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 50
	}
	if cfg.Duration == 0 {
		cfg.Duration = 2 * time.Second
	}
}

// Deployment is a simulated cluster.
type Deployment struct {
	Sim   *qenv.Sim
	Nodes []*Node
	cfg   Config
}

// Deploy builds the nodes on s: their processes, logs and services.
func Deploy(s *qenv.Sim, cfg Config) *Deployment {
	cfg.defaults()
	installElectionEntropy()
	d := &Deployment{Sim: s, cfg: cfg}
	s.Clocks = cfg.Clocks
	s.NemesisEvery = nemesisEvery
	for i, name := range Nodes {
		n := &Node{d: d, Index: i, Name: name}
		n.WAL = qenv.NewStore(s, "wal-"+name, qenv.Serializable)
		initialLog(n.WAL, len(Nodes), newSM(Keys, Initial))
		d.Nodes = append(d.Nodes, n)
	}
	for _, n := range d.Nodes {
		n.Proc = s.Process(n.Name, true, n.start)
		n.Raft = qenv.NewService(s, "raft-"+n.Name, n.Proc, n.receive)
		n.KV = qenv.NewService(s, "kv-"+n.Name, n.Proc, n.serve)
	}
	s.OnCrash(func(p *qenv.Process) {
		for _, n := range d.Nodes {
			if n.Proc == p {
				n.inc = nil
				if cfg.Record == Cluster {
					qobs.Record(s.Context(), "stop", nil, "node", n.Name)
				}
			}
		}
	})
	if cfg.Partitions {
		s.Net().PartitionNemesis(d.procs()...)
	}
	return d
}

func (d *Deployment) procs() []*qenv.Process {
	var ps []*qenv.Process
	for _, n := range d.Nodes {
		ps = append(ps, n.Proc)
	}
	return ps
}

// startAny is a node's start function found by its process: an
// upgrade's code, which names no node.
func (d *Deployment) startAny(ctx context.Context) {
	p := qenv.ProcessOf(ctx)
	for _, n := range d.Nodes {
		if n.Proc == p {
			n.start(ctx)
			return
		}
	}
}

// Isolate cuts node i off from the others, and records it when the run
// records the cluster.
func (d *Deployment) Isolate(ctx context.Context, i int) {
	for j, m := range d.Nodes {
		if j != i {
			d.Sim.Net().Partition(d.Nodes[i].Proc, m.Proc)
		}
	}
	if d.cfg.Record == Cluster {
		qobs.Record(ctx, "isolate", nil, "node", d.Nodes[i].Name)
	}
}

// Heal ends every partition, and records it when the run records the
// cluster.
func (d *Deployment) Heal(ctx context.Context) {
	for i, a := range d.Nodes {
		for _, b := range d.Nodes[i+1:] {
			d.Sim.Net().Heal(a.Proc, b.Proc)
		}
	}
	if d.cfg.Record == Cluster {
		qobs.Record(ctx, "heal", nil)
	}
}

// TransferLeader asks the leader to hand its leadership to node i, as an
// operator does before maintenance (etcd's move-leader), and waits up to
// a second for i to lead; it reports whether it does.
func (d *Deployment) TransferLeader(ctx context.Context, i int) bool {
	if l := d.Leader(); l >= 0 && l != i {
		d.Nodes[l].inc.transferTo = uint64(i + 1)
	}
	return qenv.WaitFor(ctx, func() bool { return d.Leader() == i }, time.Second)
}

// Leader is the index of a node that believes it leads, -1 if none.
func (d *Deployment) Leader() int {
	for i, n := range d.Nodes {
		if n.inc != nil && n.inc.state == raft.StateLeader {
			return i
		}
	}
	return -1
}

// Run runs the cluster under c: the clients for cfg.Duration, then a
// settle, then the probe.
func Run(c qenv.Chooser, cfg Config) error {
	_, err := RunDeployment(c, cfg)
	return err
}

// RunDeployment is Run, returning the deployment for a test to inspect
// after it (the nodes' versions).
func RunDeployment(c qenv.Chooser, cfg Config) (*Deployment, error) {
	cfg.defaults()
	s := qenv.New(c)
	defer s.Close()
	d := Deploy(s, cfg)
	if cfg.Scenario != nil {
		s.Spawn("scenario", func(ctx context.Context) { cfg.Scenario(ctx, d) })
	}
	if cfg.Rollout > 0 {
		s.Spawn("rollout", func(ctx context.Context) {
			// one node after another: down for its restart, then up on v2
			for _, n := range d.Nodes[:cfg.Rollout] {
				qenv.Sleep(ctx, 500*time.Millisecond)
				n.Proc.Upgrade("v2", d.startAny)
			}
		})
	}
	d.clients(s)
	if err := s.Run(cfg.Duration); err != nil {
		return d, err
	}
	if err := s.Settle(5 * time.Second); err != nil {
		return d, err // an *Unsettled is the run's outcome, judged
	}
	if cfg.Record == KV {
		d.Probe(s.Context())
	}
	return d, nil
}

// Probe records what each node's state machine holds of every key, then
// that the system is still settled.
func (d *Deployment) Probe(ctx context.Context) {
	for _, n := range d.Nodes {
		if n.inc == nil {
			continue
		}
		for _, k := range Keys {
			qobs.Invoke(ctx, "observe", "node", n.Name, "key", k, "value", n.inc.sm.KV[k]).Done(nil)
		}
	}
	qobs.Settled(ctx)
}

// Versions is each node's log format and code version, after a run.
func (d *Deployment) Versions() string {
	var out string
	for _, n := range d.Nodes {
		if n.inc != nil {
			out += fmt.Sprintf("%s:code v%d log v%d ", n.Name, n.inc.version, n.inc.sm.Version)
		}
	}
	return out
}
