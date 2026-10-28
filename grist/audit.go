package grist

import (
	"context"
	"crypto/sha256"
	"fmt"
)

// DefaultSnapshotAudit is how many reads from a snapshot one in is
// audited, by default (SnapshotAudit).
const DefaultSnapshotAudit = 100

// snapshotAuditKey carries SnapshotAudit's n.
type snapshotAuditKey struct{}

// SnapshotAudit is ctx with one in n reads from a snapshot audited: the
// read model's key refolded from the start of the log and compared with
// the snapshot plus its tail (DefaultSnapshotAudit without it; n <= 0
// none). Which reads is sampled by the snapshot (its read model, key and
// position, and the read's), not at random, so a simulation's runs
// replay. A read whose
// snapshot is wrong answers the refold and replaces the snapshot with it,
// and is counted: Main's /metrics serves grist_snapshot_audits_total and
// grist_snapshot_audit_mismatches_total, App.SnapshotAudits both. A
// mismatch means a fold changed without a new fingerprint (bump its
// read model's version) or a store returned another snapshot than was
// put: gristtest.Snapshots and App.CheckSnapshots check the same before a
// deploy, on a test's store; the audit checks the deployed one's.
func SnapshotAudit(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, snapshotAuditKey{}, n)
}

// SnapshotAudits are how many snapshot reads the App's nodes audited
// (SnapshotAudit) since the process started, and how many of them found
// the snapshot wrong.
func (a *App) SnapshotAudits() (audited, mismatched int64) {
	return a.audits.Load(), a.auditMisses.Load()
}

// audited reports whether the read through at from snapshot snap is one
// SnapshotAudit samples.
func audited(ctx context.Context, snap Snapshot, at Position) bool {
	n := DefaultSnapshotAudit
	if v, ok := ctx.Value(snapshotAuditKey{}).(int); ok {
		n = v
	}
	if n <= 0 {
		return false
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d", snap.Name, snap.Key, snap.Pos, at)))
	return (uint64(h[0])<<8|uint64(h[1]))%uint64(n) == 0
}
