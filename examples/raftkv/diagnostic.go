package raftkv

import (
	pb "go.etcd.io/raft/v3/raftpb"
	"log"
	"os"
	"time"
)

var diagnosticEnabled = os.Getenv("RAFTKV_DIAG") == "1"

func diagnostic(format string, args ...any) {
	if diagnosticEnabled {
		log.Printf("DIAG at=%s "+format, append([]any{time.Now().UTC().Format(time.RFC3339Nano)}, args...)...)
	}
}
func electionMessage(m *pb.Message) bool {
	return m.GetType() == pb.MessageType_MsgPreVote || m.GetType() == pb.MessageType_MsgPreVoteResp || m.GetType() == pb.MessageType_MsgVote || m.GetType() == pb.MessageType_MsgVoteResp
}
