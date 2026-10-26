package raftkv

import (
	"context"
	"fmt"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Backoff is how long a client waits before it tries an operation again
// the first time; it doubles with every attempt, up to eight times it, so
// a few attempts outlast an election.
const Backoff = 20 * time.Millisecond

// clients starts the run's clients, tasks of no process (a partition
// between nodes does not reach them).
func (d *Deployment) clients(s *qenv.Sim) {
	cfg := d.cfg
	unrecorded := func(ctx context.Context) context.Context { return ctx }
	if cfg.Record != KV {
		if !cfg.Load {
			return
		}
		// the clients write, so the logs grow and compact, but their calls
		// are no steps of the cluster's model: into a recorder of their own
		unrecorded = func(ctx context.Context) context.Context {
			return qobs.WithRecorder(ctx, qobs.NewRecorder(dropSteps{}))
		}
	}
	if cfg.Readers {
		for i := range d.Nodes {
			s.Spawn(fmt.Sprintf("reader%d", i+1), func(ctx context.Context) {
				for op := range cfg.Ops {
					d.do(ctx, i+1, op+1, Op{Kind: "get", Key: Keys[0]}, i)
					qenv.Sleep(ctx, 50*time.Millisecond)
				}
			})
		}
		w := len(d.Nodes) + 1
		s.Spawn("writer", func(ctx context.Context) {
			for op := range cfg.Ops {
				d.do(ctx, w, op+1, Op{Kind: "put", Key: Keys[0], A: 100 + op}, -1)
				qenv.Sleep(ctx, 50*time.Millisecond)
			}
		})
		return
	}
	for id := 1; id <= cfg.Clients; id++ {
		s.Spawn(fmt.Sprintf("client%d", id), func(ctx context.Context) {
			ctx = unrecorded(ctx)
			for k := range cfg.Ops {
				d.do(ctx, id, k+1, Workload(id, k), -1)
				qenv.Sleep(ctx, cfg.Think)
			}
		})
	}
}

// dropSteps is a sink that keeps nothing: the clients' calls of a run
// that records the cluster (Config.Load).
type dropSteps struct{}

func (dropSteps) Record(context.Context, *qtrace.Step) {}

// Workload is client id's k-th operation: a mix over the keys, each
// write's value its own (a stale read shows), transfers between keys.
func Workload(id, k int) Op {
	key, other := Keys[(id+k)%len(Keys)], Keys[(id+k+1)%len(Keys)]
	switch (id + k) % 4 {
	case 0:
		return Op{Kind: "put", Key: key, A: id*100 + k}
	case 1:
		return Op{Kind: "get", Key: key}
	case 2:
		return Op{Kind: "transfer", Key: key, Dest: other, A: 3}
	default:
		return Op{Kind: "cas", Key: key, A: Initial, B: id*100 + k}
	}
}

// Client is how a client reaches the cluster: in a simulation the
// deployment's (Deployment.do), in the real tier the HTTP client
// (realnet.go) over the same Do, so a real client carries its mutant
// (NoRequestID) as a simulated one does.
type Client struct {
	Net   KVNet
	Nodes int // how many nodes there are
	// MaxAttempts is how many times an operation is tried before the
	// client gives up; 50 if 0
	MaxAttempts int
	Mutation    Mutation
}

func (d *Deployment) do(ctx context.Context, id, seq int, op Op, first int) {
	Client{Net: d.kv, Nodes: len(d.Nodes), MaxAttempts: d.cfg.MaxAttempts, Mutation: d.cfg.Mutation}.Do(ctx, id, seq, op, first)
}

// Do is one operation of client id, its seq-th: a call recorded when
// invoked and when answered. It tries the node the last answer named
// leader, else the next node, up to MaxAttempts times; a client that gives
// up never answers, and its call stays pending, its effect unknown. The
// request id (id, seq) makes a retry apply once: NoRequestID gives each
// attempt a new one. It asks node first first (-1: one by its id).
func (c Client) Do(ctx context.Context, id, seq int, op Op, first int) {
	attempts := c.MaxAttempts
	if attempts == 0 {
		attempts = 50
	}
	var call *qobs.Call
	switch op.Kind {
	case "put":
		call = qobs.Invoke(ctx, "write", "key", op.Key, "value", op.A)
	case "get":
		call = qobs.Invoke(ctx, "read", "key", op.Key)
	case "cas":
		call = qobs.Invoke(ctx, "cas", "key", op.Key, "expected", op.A, "desired", op.B)
	case "transfer":
		call = qobs.Invoke(ctx, "transfer", "src", op.Key, "dest", op.Dest, "amount", op.A)
	}
	op.Client, op.Seq = id, seq*1000 // room for NoRequestID's fresh ids
	node := id % c.Nodes
	if first >= 0 {
		node = first
	}
	for attempt := range attempts {
		if attempt > 0 && c.Mutation == NoRequestID {
			op.Seq++
		}
		r, err := c.Net.Call(ctx, node, Request{Op: op}, OpTimeout+RPCTimeout)
		if err == nil && r.Err == "" {
			switch op.Kind {
			case "get":
				call.Done(nil, "obs.val", r.Result.Val)
			case "put":
				call.Done(nil)
			default:
				call.Done(nil, "obs.ok", r.Result.OK)
			}
			return
		}
		if err == nil && r.Leader >= 0 && r.Leader != node {
			node = r.Leader
		} else {
			node = (node + 1) % c.Nodes // a timeout: another endpoint, as a client's balancer does
		}
		qenv.Sleep(ctx, min(Backoff<<attempt, 8*Backoff))
	}
}
