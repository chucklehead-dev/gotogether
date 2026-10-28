package payments

import (
	"context"
	"log/slog"
)

// audit writes the request's audit record, under the lock, so the log's
// order is the order requests took effect. Each record carries the
// process's sequence number (audit_seq, from 1 at start) and its instance:
// a reader can tell a rotated or truncated log from a whole one.
//
//	{"time":...,"level":"INFO","msg":"charge.refund","audit_seq":12,"instance":"payments-1",
//	 "charge_id":"ch_4","amount":500,"result":"ok","refunded":1200}
func (s *Service) audit(ctx context.Context, msg, id, result string, attrs ...any) {
	s.auditSeq++
	args := append([]any{"audit_seq", s.auditSeq, "instance", s.cfg.Instance, "charge_id", id, "result", result}, attrs...)
	s.log.Log(ctx, slog.LevelInfo, msg, args...)
}
