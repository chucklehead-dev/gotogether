package raftkv

import (
	"context"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// The node's seams: where its messages, its clients' requests and its log
// leave the process. In a simulation each is a qenv double (simNet, a
// qenv.Store); in the real tier (cmd/raftkv-node) TCP and a file
// (realnet.go, boltwal.go). The node's code is the same on both.

// PeerNet carries a node's raft messages to its peers: a batch per
// Send, to the peer of index to. An error loses the batch (raft resends
// what matters), and the node reports the peer unreachable.
type PeerNet interface {
	Send(ctx context.Context, to int, env Envelope) error
}

// KVNet carries a client's request to the node of index node and waits up
// to timeout for its response.
type KVNet interface {
	Call(ctx context.Context, node int, req Request, timeout time.Duration) (Response, error)
}

// WAL is a node's write-ahead log: a transactional store (written
// through qenv.Retry) whose committed state the node reads at boot, and
// which a new cluster's nodes start with (Load).
type WAL interface {
	qenv.DB
	// Read is key's committed value, outside any transaction.
	Read(key string) (any, bool)
	// Keys are the committed keys with prefix, sorted.
	Keys(prefix string) []string
	// Load writes key outside any transaction, before the node starts.
	Load(key string, val any)
}

var _ WAL = (*qenv.Store)(nil)

// simNet is both networks in a simulation: the nodes' services, each a
// lossy qenv.Service in its node's process, a Latency away.
type simNet struct{ d *Deployment }

func (s simNet) Send(ctx context.Context, to int, env Envelope) error {
	qenv.Sleep(ctx, Latency)
	_, err := s.d.Nodes[to].raftSvc.Call(ctx, env, RPCTimeout)
	return err
}

func (s simNet) Call(ctx context.Context, node int, req Request, timeout time.Duration) (Response, error) {
	qenv.Sleep(ctx, Latency)
	return s.d.Nodes[node].kvSvc.Call(ctx, req, timeout)
}
