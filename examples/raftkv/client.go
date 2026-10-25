package raftkv

import (
	"context"
	"fmt"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Backoff is how long a client waits before it tries an operation again
// the first time; it doubles with every attempt, up to eight times it, so
// a few attempts outlast an election.
const Backoff = 20 * time.Millisecond

// clients starts the run's clients, tasks of no process (a partition
// between nodes does not reach them).
func (d *Deployment) clients(s *qenv.Sim) {
	cfg := d.cfg
	if cfg.Record != KV {
		return
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
			for k := range cfg.Ops {
				d.do(ctx, id, k+1, workload(id, k), -1)
				qenv.Sleep(ctx, cfg.Think)
			}
		})
	}
}

// workload is client id's k-th operation: a mix over the keys, each
// write's value its own (a stale read shows), transfers between keys.
func workload(id, k int) Op {
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

// do is one operation of client id, its seq-th: a call recorded when
// invoked and when answered. It tries the node the last answer named
// leader, else the next node, up to MaxAttempts times; a client that gives
// up never answers, and its call stays pending, its effect unknown. The
// request id (id, seq) makes a retry apply once: NoRequestID gives each
// attempt a new one. It asks node first first (-1: one by its id).
func (d *Deployment) do(ctx context.Context, id, seq int, op Op, first int) {
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
	node := id % len(d.Nodes)
	if first >= 0 {
		node = first
	}
	for attempt := range d.cfg.MaxAttempts {
		if attempt > 0 && d.cfg.Mutation == NoRequestID {
			op.Seq++
		}
		qenv.Sleep(ctx, Latency)
		r, err := d.Nodes[node].KV.Call(ctx, Request{Op: op}, OpTimeout+RPCTimeout)
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
			node = (node + 1) % len(d.Nodes) // a timeout: another endpoint, as a client's balancer does
		}
		qenv.Sleep(ctx, min(Backoff<<attempt, 8*Backoff))
	}
}
