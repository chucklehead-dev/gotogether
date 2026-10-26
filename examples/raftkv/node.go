package raftkv

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"go.etcd.io/raft/v3/tracker"
	"google.golang.org/protobuf/proto"
)

// Timing, in virtual time.
const (
	Tick       = 10 * time.Millisecond // a raft tick
	Latency    = time.Millisecond      // a message's time on the network
	RPCTimeout = 20 * time.Millisecond // a raft message's call
	OpTimeout  = 100 * time.Millisecond
	// ElectionTicks is how long a follower waits for its leader before it
	// campaigns (and up to twice it, randomized); a heartbeat goes out
	// every tick.
	ElectionTicks = 10
	// compactEvery is how many applied entries a node keeps in its log
	// before it snapshots its state machine and drops them.
	compactEvery = 16
)

// Envelope is a batch of raft messages from one node to another, as the
// wire carries them: marshalled, with the version of the code that sent
// them, which is how a leader learns its peers can read the v2 log.
type Envelope struct {
	From    int // the sender's index
	Version int
	Msgs    [][]byte
}

// Request is a client's operation, sent to a node.
type Request struct{ Op Op }

// Response is a node's answer: the result, or why there is none (not the
// leader, which it names if it knows, or the operation timed out).
type Response struct {
	Result Result
	Err    string
	Leader int // the leader's index, -1 if unknown
}

// Node is one replica: a crashable process, its write-ahead log
// (durable across crashes), the network to its peers, and its volatile
// state, rebuilt from the log at every start. In a simulation peers and
// clients call its services (raftSvc, kvSvc); in the real tier its HTTP
// server (realnet.go) calls the same handlers.
type Node struct {
	d     *Deployment
	Index int
	Name  string
	Proc  *qenv.Process // nil in the real tier
	WAL   WAL
	Peers PeerNet
	// version is the code's outside a simulation ("v2" or not), where no
	// qenv.Process carries one
	version string
	raftSvc *qenv.Service[Envelope, struct{}]
	kvSvc   *qenv.Service[Request, Response]
	// inc is nil while down. Atomic: in the real tier the HTTP handlers
	// read it while the node boots.
	inc atomic.Pointer[incarnation]
}

// incarnation is a node's state from one start to its crash: raft's,
// the state machine rebuilt from the log, and the work waiting for the
// loop, which alone touches the RawNode.
//
// mu guards what the loop, the senders and the handlers share (every
// field below it but the loop's own: lastIndex, snapIndex and the
// MemoryStorage, which has its lock). A simulation runs one task at a
// time between seams, so there it is never contended; in the real tier
// they are goroutines. It is never held across a seam (a wait, a call, a
// transaction): a simulated task frozen there would hold it, and the
// schedule would change. qenv.Wait's and WaitFor's predicates take it.
type incarnation struct {
	version   int // the code's: 1, or 2 after an upgrade; set at boot
	ms        *raft.MemoryStorage
	snapIndex uint64
	lastIndex uint64 // the last index in the write-ahead log

	mu      sync.Mutex
	rn      *raft.RawNode
	cs      *pb.ConfState
	sm      *SM
	applied uint64

	inbox      []*pb.Message
	props      [][]byte
	transferTo uint64 // a leadership transfer the loop is to start (raft's ID)
	// failed are the peers (raft's IDs) a send to failed, and whether the
	// batch held a snapshot: the loop reports them to raft, which otherwise
	// waits on a lost snapshot forever (its progress paused)
	failed    []sendFailure
	snapWait  map[uint64]int // a peer's ticks in StateSnapshot, the leader's
	reads     [][]byte
	readIndex map[uint64]uint64 // a read's id to the index it must see applied
	nextRead  uint64
	outbox    [][]*pb.Message // by peer index

	state       raft.StateType
	lead        uint64
	ledTerm     uint64 // the term this node last led, 0 if none
	campaigned  uint64 // the term this node last campaigned for
	peerVersion []int
	versionAt   time.Duration // when the leader last proposed the switch to v2
}

func (inc *incarnation) work() bool {
	inc.mu.Lock()
	defer inc.mu.Unlock()
	return len(inc.inbox) > 0 || len(inc.props) > 0 || len(inc.reads) > 0 || inc.transferTo != 0 || len(inc.failed) > 0
}

// locked is ready under mu: a wait's predicate.
func (inc *incarnation) locked(ready func() bool) func() bool {
	return func() bool {
		inc.mu.Lock()
		defer inc.mu.Unlock()
		return ready()
	}
}

type sendFailure struct {
	to   uint64
	snap bool
}

// writeVersion is the format this node writes: v1 until the cluster has
// committed the switch to v2, but by EagerV2, whose v2 code writes v2 at
// once, into a log v1 nodes still read.
func (inc *incarnation) writeVersion(m Mutation) int {
	if inc.version >= 2 && (inc.sm.Version >= 2 || m == EagerV2) {
		return 2
	}
	return 1
}

func (inc *incarnation) encode(op Op, m Mutation) []byte {
	if inc.writeVersion(m) == 2 {
		return encodeV2(op)
	}
	return encodeV1(op)
}

// start is the node's process: rebuild from the log, start a sender for
// each peer, and run the raft loop.
func (n *Node) start(ctx context.Context) {
	inc := n.boot(ctx)
	n.inc.Store(inc)
	if n.d.cfg.Record == Cluster {
		qobs.Record(ctx, "boot", nil, "node", n.Name, "index", int(inc.applied))
	}
	for i := range n.d.Nodes {
		if i != n.Index {
			qenv.Go(ctx, fmt.Sprintf("%s.send.%s", n.Name, n.d.Nodes[i].Name), func(ctx context.Context) { n.send(ctx, inc, i) })
		}
	}
	n.loop(ctx, inc)
}

// boot reads the write-ahead log into raft's storage and the state
// machine, at the snapshot's index: raft hands the committed entries after
// it back to be applied again.
func (n *Node) boot(ctx context.Context) *incarnation {
	inc := &incarnation{version: 1, ms: raft.NewMemoryStorage(), readIndex: map[uint64]uint64{},
		outbox: make([][]*pb.Message, len(n.d.Nodes)), peerVersion: make([]int, len(n.d.Nodes)), lead: raft.None}
	if p := qenv.ProcessOf(ctx); p != nil && p.Version == "v2" || p == nil && n.version == "v2" {
		inc.version = 2
	}
	snap := &pb.Snapshot{}
	b, _ := n.WAL.Read("snap")
	must(proto.Unmarshal(b.([]byte), snap))
	must(inc.ms.ApplySnapshot(snap))
	inc.cs = snap.GetMetadata().GetConfState()
	inc.sm = decodeSM(snap.GetData())
	inc.applied, inc.snapIndex = snap.GetMetadata().GetIndex(), snap.GetMetadata().GetIndex()
	inc.lastIndex = inc.snapIndex
	if b, ok := n.WAL.Read("hs"); ok {
		hs := &pb.HardState{}
		must(proto.Unmarshal(b.([]byte), hs))
		must(inc.ms.SetHardState(hs))
	}
	var ents []*pb.Entry
	for _, k := range n.WAL.Keys("e/") { // in key order: the index's
		b, _ := n.WAL.Read(k)
		e := &pb.Entry{}
		must(proto.Unmarshal(b.([]byte), e))
		if e.GetIndex() > inc.snapIndex {
			ents = append(ents, e)
			inc.lastIndex = e.GetIndex()
		}
	}
	must(inc.ms.Append(ents))
	cfg := &raft.Config{
		ID:              uint64(n.Index + 1),
		ElectionTick:    ElectionTicks,
		HeartbeatTick:   1,
		Storage:         inc.ms,
		Applied:         inc.applied,
		MaxSizePerMsg:   1 << 20,
		MaxInflightMsgs: 64,
		CheckQuorum:     true,
		PreVote:         true,
		ReadOnlyOption:  raft.ReadOnlySafe,
		Logger:          discard{},
	}
	switch n.d.cfg.Mutation {
	case LeaseRead:
		cfg.ReadOnlyOption = raft.ReadOnlyLeaseBased
	case SlowElection:
		cfg.ElectionTick = 10 * ElectionTicks
	}
	rn, err := raft.NewRawNode(cfg)
	must(err)
	inc.rn = rn
	return inc
}

// loop is the node's raft loop, the RawNode's only user: tick on the
// node's own clock, hand raft what arrived, and handle what it is ready
// to do.
func (n *Node) loop(ctx context.Context, inc *incarnation) {
	qenv.Daemon(ctx)
	next := qenv.Mono(ctx) + Tick
	for {
		if wait := next - qenv.Mono(ctx); wait > 0 {
			qenv.WaitFor(ctx, inc.work, wait)
		}
		now := qenv.Mono(ctx)
		inc.mu.Lock()
		if now >= next {
			inc.rn.Tick()
			next += Tick
			if next <= now { // a pause: tick once, not for every tick missed
				next = now + Tick
			}
			n.proposeVersion(now, inc)
			n.retrySnapshots(inc)
		}
		inbox, props, reads := inc.inbox, inc.props, inc.reads
		inc.inbox, inc.props, inc.reads = nil, nil, nil
		for _, m := range inbox {
			_ = inc.rn.Step(m) // a message raft refuses (a stale term's) is dropped, as raft wants
		}
		for _, p := range props {
			_ = inc.rn.Propose(p) // dropped without a leader: the client times out and retries
		}
		for _, r := range reads {
			inc.rn.ReadIndex(r)
		}
		if inc.transferTo != 0 {
			inc.rn.TransferLeader(inc.transferTo)
			inc.transferTo = 0
		}
		for _, f := range inc.failed {
			inc.rn.ReportUnreachable(f.to)
			if f.snap && n.d.cfg.Mutation != NoSnapshotReport {
				inc.rn.ReportSnapshot(f.to, raft.SnapshotFailure)
			}
		}
		inc.failed = nil
		for inc.rn.HasReady() {
			n.ready(ctx, inc, inc.rn.Ready())
		}
		inc.mu.Unlock()
	}
}

// ready handles one Ready in the order raft requires: persist the hard
// state, the entries and any snapshot, then send, then apply. It is
// called with inc.mu held and releases it while it writes the log.
func (n *Node) ready(ctx context.Context, inc *incarnation, rd raft.Ready) {
	inc.mu.Unlock()
	n.persist(ctx, inc, rd)
	inc.mu.Lock()
	if rd.SoftState != nil {
		inc.state, inc.lead = rd.SoftState.RaftState, rd.SoftState.Lead
	}
	n.recordLeadership(ctx, inc)
	for _, m := range rd.Messages {
		to := int(m.GetTo()) - 1
		q := append(inc.outbox[to], m)
		if len(q) > 256 { // a peer long unreachable: raft resends what matters
			q = q[len(q)-256:]
		}
		inc.outbox[to] = q
	}
	before := inc.applied
	if !raft.IsEmptySnap(rd.Snapshot) {
		inc.sm = decodeSM(rd.Snapshot.GetData())
		inc.applied = rd.Snapshot.GetMetadata().GetIndex()
	}
	for _, e := range rd.CommittedEntries {
		if e.GetType() == pb.EntryType_EntryNormal && len(e.GetData()) > 0 {
			if op, err := decode(e.GetData(), inc.version >= 2); err == nil {
				inc.sm.apply(op)
			}
		}
		inc.applied = e.GetIndex()
	}
	if n.d.cfg.Record == Cluster && inc.applied != before {
		qobs.Record(ctx, "apply", nil, "node", n.Name, "index", int(inc.applied))
	}
	for _, rs := range rd.ReadStates {
		inc.readIndex[binary.BigEndian.Uint64(rs.RequestCtx)] = rs.Index
	}
	inc.rn.Advance(rd)
	n.compact(ctx, inc)
}

// recordLeadership records the node's steps of the cluster model when a
// run records them (Config.Record): a campaign for a new term, leading a
// term, stepping down from one. They are recorded after the hard state
// that carries the term is persisted, and before any message of it is
// sent.
func (n *Node) recordLeadership(ctx context.Context, inc *incarnation) {
	if n.d.cfg.Record != Cluster {
		return
	}
	term := inc.rn.BasicStatus().GetTerm()
	if inc.ledTerm != 0 && (inc.state != raft.StateLeader || term != inc.ledTerm) {
		qobs.Record(ctx, "stepDown", nil, "node", n.Name, "term", int(inc.ledTerm))
		inc.ledTerm = 0
	}
	if inc.state == raft.StateCandidate && term != inc.campaigned {
		inc.campaigned = term
		qobs.Record(ctx, "campaign", nil, "node", n.Name, "term", int(term))
	}
	if inc.state == raft.StateLeader && inc.ledTerm == 0 {
		inc.ledTerm = term
		qobs.Record(ctx, "lead", nil, "node", n.Name, "term", int(term))
	}
}

// proposeVersion is the leader's switch of the log to v2, once every node
// runs v2 (each says so in its messages): a committed entry, after which
// every node writes v2. Until then the cluster writes v1, which every
// version reads.
func (n *Node) proposeVersion(now time.Duration, inc *incarnation) {
	if inc.version < 2 || inc.sm.Version >= 2 || inc.state != raft.StateLeader {
		return
	}
	for i, v := range inc.peerVersion {
		if i != n.Index && v < 2 {
			return
		}
	}
	if inc.versionAt == 0 || now-inc.versionAt > OpTimeout {
		inc.versionAt = now
		inc.props = append(inc.props, encodeV1(Op{Kind: "version", A: 2}))
	}
}

// retrySnapshots reports a snapshot failed when a peer has waited for one
// for two election timeouts: a snapshot can be lost after its call
// succeeded (in the peer's inbox when it crashed), and raft resends one
// only after a failure is reported.
func (n *Node) retrySnapshots(inc *incarnation) {
	if inc.state != raft.StateLeader || n.d.cfg.Mutation == NoSnapshotReport {
		inc.snapWait = nil
		return
	}
	if inc.snapWait == nil {
		inc.snapWait = map[uint64]int{}
	}
	var stuck []uint64
	inc.rn.WithProgress(func(id uint64, _ raft.ProgressType, pr tracker.Progress) {
		if pr.State != tracker.StateSnapshot {
			delete(inc.snapWait, id)
			return
		}
		if inc.snapWait[id]++; inc.snapWait[id] > 2*ElectionTicks {
			stuck = append(stuck, id)
		}
	})
	slices.Sort(stuck) // WithProgress visits a map
	for _, id := range stuck {
		delete(inc.snapWait, id)
		inc.rn.ReportSnapshot(id, raft.SnapshotFailure)
	}
}

// send delivers the node's messages to one peer, a batch per call; a
// call that fails loses its batch, which raft tolerates.
func (n *Node) send(ctx context.Context, inc *incarnation, to int) {
	qenv.Daemon(ctx)
	for {
		qenv.Wait(ctx, inc.locked(func() bool { return len(inc.outbox[to]) > 0 }))
		inc.mu.Lock()
		msgs := inc.outbox[to]
		inc.outbox[to] = nil
		inc.mu.Unlock()
		env := Envelope{From: n.Index, Version: inc.version}
		for _, m := range msgs {
			b, err := proto.Marshal(m)
			must(err)
			env.Msgs = append(env.Msgs, b)
		}
		if err := n.Peers.Send(ctx, to, env); err != nil {
			snap := slices.ContainsFunc(msgs, func(m *pb.Message) bool { return m.GetType() == pb.MessageType_MsgSnap })
			inc.mu.Lock()
			inc.failed = append(inc.failed, sendFailure{to: uint64(to + 1), snap: snap})
			inc.mu.Unlock()
		}
	}
}

// receive is the raft service's handler: the messages go to the loop.
func (n *Node) receive(ctx context.Context, env Envelope) (struct{}, error) {
	inc := n.inc.Load()
	if inc == nil {
		return struct{}{}, errDown // booting: refused, as a node not yet listening does
	}
	msgs := make([]*pb.Message, 0, len(env.Msgs))
	for _, b := range env.Msgs {
		m := &pb.Message{}
		if err := proto.Unmarshal(b, m); err != nil {
			return struct{}{}, err // only a real peer's bytes can be garbled
		}
		msgs = append(msgs, m)
	}
	inc.mu.Lock()
	defer inc.mu.Unlock()
	inc.peerVersion[env.From] = max(inc.peerVersion[env.From], env.Version)
	inc.inbox = append(inc.inbox, msgs...)
	return struct{}{}, nil
}

// serve is the client service's handler. Only the leader serves: a write
// is proposed and answered once applied (its result is its session's, so
// a retry answers the same); a read waits for raft's read index to be
// applied, which the leader confirms with a quorum (ReadOnlySafe) or, by
// LeaseRead, with its lease.
func (n *Node) serve(ctx context.Context, req Request) (Response, error) {
	inc := n.inc.Load()
	if inc == nil {
		return Response{Err: "not leader", Leader: -1}, nil
	}
	inc.mu.Lock()
	if inc.state != raft.StateLeader {
		lead := -1
		if inc.lead != raft.None {
			lead = int(inc.lead) - 1
		}
		inc.mu.Unlock()
		return Response{Err: "not leader", Leader: lead}, nil
	}
	op := req.Op
	if op.Kind == "get" {
		id := inc.nextRead
		inc.nextRead++
		rctx := binary.BigEndian.AppendUint64(nil, id)
		inc.reads = append(inc.reads, rctx)
		inc.mu.Unlock()
		ok := qenv.WaitFor(ctx, inc.locked(func() bool {
			idx, ok := inc.readIndex[id]
			return ok && inc.applied >= idx
		}), OpTimeout)
		inc.mu.Lock()
		defer inc.mu.Unlock()
		delete(inc.readIndex, id)
		if !ok {
			return Response{Err: "timeout", Leader: -1}, nil
		}
		return Response{Result: Result{OK: true, Val: inc.sm.KV[op.Key]}}, nil
	}
	inc.props = append(inc.props, inc.encode(op, n.d.cfg.Mutation))
	inc.mu.Unlock()
	if !qenv.WaitFor(ctx, inc.locked(func() bool { return inc.sm.Sessions[op.Client].Seq >= op.Seq }), OpTimeout) {
		return Response{Err: "timeout", Leader: -1}, nil
	}
	inc.mu.Lock()
	defer inc.mu.Unlock()
	return Response{Result: inc.sm.Sessions[op.Client].Result}, nil
}

var errDown = errors.New("raftkv: node not up")

// leads is whether the node is up and believes it leads.
func (n *Node) leads() bool {
	inc := n.inc.Load()
	if inc == nil {
		return false
	}
	inc.mu.Lock()
	defer inc.mu.Unlock()
	return inc.state == raft.StateLeader
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// discard is raft's logger, silent: a Fatal or Panic still panics.
type discard struct{}

func (discard) Debug(...any)            {}
func (discard) Debugf(string, ...any)   {}
func (discard) Error(...any)            {}
func (discard) Errorf(string, ...any)   {}
func (discard) Info(...any)             {}
func (discard) Infof(string, ...any)    {}
func (discard) Warning(...any)          {}
func (discard) Warningf(string, ...any) {}
func (discard) Fatal(v ...any)          { panic(fmt.Sprint(v...)) }
func (discard) Fatalf(f string, v ...any) {
	panic(fmt.Sprintf(f, v...))
}
func (discard) Panic(v ...any)            { panic(fmt.Sprint(v...)) }
func (discard) Panicf(f string, v ...any) { panic(fmt.Sprintf(f, v...)) }
