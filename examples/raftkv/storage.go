package raftkv

import (
	"context"
	"fmt"

	"github.com/chucklehead-dev/gotogether/qenv"
	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

// The write-ahead log is a Store of a node's own: its hard state ("hs"),
// its last snapshot ("snap") and its entries after it ("e/<index>", in
// index order), each a marshalled protobuf.

func entryKey(i uint64) string { return fmt.Sprintf("e/%012d", i) }

func marshal(m proto.Message) []byte {
	b, err := proto.Marshal(m)
	must(err)
	return b
}

// persist writes a Ready's hard state, entries and snapshot to the log
// in one transaction, then to raft's storage: before any of its messages
// is sent, as raft requires (a vote not on disk can be cast twice). An
// appended entry replaces the log's suffix from its index on. By
// NoPersistVote the hard state is written without its vote.
func (n *Node) persist(ctx context.Context, inc *incarnation, rd raft.Ready) {
	snap := !raft.IsEmptySnap(rd.Snapshot)
	if rd.HardState == nil && len(rd.Entries) == 0 && !snap {
		return
	}
	last := inc.lastIndex
	err := qenv.Retry(ctx, n.WAL, 5, func(tx qenv.Txn) error {
		if rd.HardState != nil {
			hs := proto.Clone(rd.HardState).(*pb.HardState)
			if n.d.cfg.Mutation == NoPersistVote {
				hs.Vote = nil
			}
			tx.Put("hs", marshal(hs))
		}
		from := inc.snapIndex + 1
		if snap {
			tx.Put("snap", marshal(rd.Snapshot))
			from = 0 // the snapshot replaces the whole log
		}
		if len(rd.Entries) > 0 && !snap {
			from = rd.Entries[0].GetIndex()
		}
		if snap || len(rd.Entries) > 0 {
			for i := max(from, inc.snapIndex+1); i <= last; i++ {
				tx.Delete(entryKey(i))
			}
		}
		for _, e := range rd.Entries {
			tx.Put(entryKey(e.GetIndex()), marshal(e))
		}
		return nil
	})
	if err != nil {
		// a node that cannot write its log stops, as a real one exits
		n.Proc.Crash()
	}
	if snap {
		must(inc.ms.ApplySnapshot(rd.Snapshot))
		inc.snapIndex = rd.Snapshot.GetMetadata().GetIndex()
		inc.lastIndex = inc.snapIndex
	}
	if len(rd.Entries) > 0 {
		must(inc.ms.Append(rd.Entries))
		inc.lastIndex = rd.Entries[len(rd.Entries)-1].GetIndex()
	}
	if rd.HardState != nil {
		must(inc.ms.SetHardState(rd.HardState))
	}
}

// compact snapshots the state machine once compactEvery entries were
// applied since the last snapshot, and drops them from the log.
func (n *Node) compact(ctx context.Context, inc *incarnation) {
	if inc.applied < inc.snapIndex+compactEvery {
		return
	}
	s, err := inc.ms.CreateSnapshot(inc.applied, inc.cs, inc.sm.encode())
	must(err)
	must(inc.ms.Compact(inc.applied))
	old := inc.snapIndex
	if err := qenv.Retry(ctx, n.WAL, 5, func(tx qenv.Txn) error {
		tx.Put("snap", marshal(s))
		for i := old + 1; i <= inc.applied; i++ {
			tx.Delete(entryKey(i))
		}
		return nil
	}); err != nil {
		n.Proc.Crash()
	}
	inc.snapIndex = inc.applied
}

// initialLog is every node's log when the cluster is created: a snapshot
// at index 1 of the three voters and the keys at their initial value, so
// no node needs raft's bootstrap entries.
func initialLog(wal *qenv.Store, voters int, sm *SM) {
	cs := &pb.ConfState{}
	for i := range voters {
		cs.Voters = append(cs.Voters, uint64(i+1))
	}
	snap := &pb.Snapshot{Data: sm.encode(), Metadata: &pb.SnapshotMetadata{ConfState: cs, Index: new(uint64(1)), Term: new(uint64(1))}}
	wal.Load("snap", marshal(snap))
	wal.Load("hs", marshal(&pb.HardState{Term: new(uint64(1)), Commit: new(uint64(1))}))
}
