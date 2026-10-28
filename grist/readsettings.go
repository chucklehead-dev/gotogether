package grist

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync/atomic"

	"github.com/chucklehead-dev/gotogether/grist/internal/readcfg"
)

// DefaultSnapshotEvery is how many events a read folds before it takes a
// snapshot, by default (Snapshots).
const DefaultSnapshotEvery = 1000

// DefaultSnapshotAudit is how many reads from a snapshot or the memo one
// in is audited, by default (AuditSnapshots).
const DefaultSnapshotAudit = 100

// DefaultMemoEntries is how many read models' states a node keeps in
// memory per store, by default (Memo).
const DefaultMemoEntries = 4096

// An AppOption configures an App (New).
type AppOption struct{ apply func(*App) }

// Snapshots makes the App's reads take a read model's snapshot once they
// folded every events past the last one (DefaultSnapshotEvery without
// it), and keep its state in the memo (Memo); every <= 0 turns both off:
// every read folds from the start of the log. A store that keeps no
// snapshots (no store.SnapshotStore) has none, and the memo still serves.
// gristsim's Config.SnapshotEvery overrides it for one simulation.
func Snapshots(every int) AppOption {
	return AppOption{apply: func(a *App) { a.every = every }}
}

// AuditSnapshots makes one in oneIn reads from a snapshot or the memo an
// audit (DefaultSnapshotAudit without it; oneIn <= 0 none): the read
// model's key refolded from the start of the log, through the same
// position and selector, and compared. Which reads is sampled by a hash
// of the snapshot's read model, key and position and the read's, not at
// random, so a simulation's runs replay. A wrong state is answered by the
// refold, replaced by it, and counted (ReadStats, Main's /metrics): a
// fold changed without a new fingerprint (bump its read model's
// version), or a store returning another snapshot than was put.
// gristtest.Snapshots and App.CheckSnapshots check the same before a
// deploy, on a test's store; the audit checks the deployed one's.
func AuditSnapshots(oneIn int) AppOption {
	return AppOption{apply: func(a *App) { a.audit = oneIn }}
}

// Memo makes each node keep up to entries read models' states in memory,
// per store (DefaultMemoEntries without it; entries <= 0 none): a read of
// one it holds reads only the events after the state's (its last folded
// event, read again to check it is still there: a log rewound and
// refilled, ErrRewound, invalidates it), not a snapshot and its tail. A
// state is memoized only by a read that folded Snapshots' every events or
// read a snapshot, so a short log's reads are as they are without it. A
// least recently used state goes first.
func Memo(entries int) AppOption {
	return AppOption{apply: func(a *App) { a.memoSize = entries }}
}

// snapshotEvery is the snapshot cadence of reads under ctx.
func (a *App) snapshotEvery(ctx context.Context) int {
	if s := readcfg.Of(ctx); s.HasEvery {
		return s.Every
	}
	return a.every
}

// auditEvery is the audit rate of reads under ctx.
func (a *App) auditEvery(ctx context.Context) int {
	if s := readcfg.Of(ctx); s.HasAudit {
		return s.Audit
	}
	return a.audit
}

// memoEntries is the memo's size for reads under ctx.
func (a *App) memoEntries(ctx context.Context) int {
	if s := readcfg.Of(ctx); s.HasMemo {
		return s.Memo
	}
	return a.memoSize
}

// audited reports whether a read through at from a state anchored at
// anchor (a snapshot's or the memo's) of name and key is one the App's
// audit samples.
func (a *App) audited(ctx context.Context, name, key string, anchor, at Position) bool {
	n := a.auditEvery(ctx)
	if n <= 0 {
		return false
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%d", name, key, anchor, at)))
	return (uint64(h[0])<<8|uint64(h[1]))%uint64(n) == 0
}

// readStats are the App's read counters (ReadStats).
type readStats struct {
	folded, memoHits, memoStale, snapshotHits, snapshotMisses, putFailures, pruned, audits, auditMisses atomic.Int64
}

// ReadStats are an App's reads since the process started, as Main's
// /metrics serves them: what a read costs, and whether the caches serve.
type ReadStats struct {
	// Folded counts the events read models folded (grist_fold_events_total).
	Folded int64
	// MemoHits counts reads served from the memo (grist_memo_hits_total),
	// MemoStale those whose memoized state was no longer the log's: its
	// last event gone (a rewind), refolded (grist_memo_stale_total).
	MemoHits, MemoStale int64
	// SnapshotHits and SnapshotMisses count snapshot lookups that found a
	// sound one and those that found none or an unsound one: torn,
	// another fold's, past the read, or its last event gone
	// (grist_snapshot_hits_total, grist_snapshot_misses_total).
	SnapshotHits, SnapshotMisses int64
	// SnapshotPutFailures counts puts the store failed (a read-only role,
	// a full disk): reads go on folding the log
	// (grist_snapshot_put_failures_total).
	SnapshotPutFailures int64
	// SnapshotsPruned counts other fingerprints' snapshots deleted after
	// a put (grist_snapshots_pruned_total).
	SnapshotsPruned int64
	// Audits counts audited reads (AuditSnapshots), AuditMismatches those
	// whose state was not the refold's (grist_snapshot_audits_total,
	// grist_snapshot_audit_mismatches_total).
	Audits, AuditMismatches int64
}

// ReadStats are the App's read counters.
func (a *App) ReadStats() ReadStats {
	s := &a.stats
	return ReadStats{Folded: s.folded.Load(), MemoHits: s.memoHits.Load(), MemoStale: s.memoStale.Load(),
		SnapshotHits: s.snapshotHits.Load(), SnapshotMisses: s.snapshotMisses.Load(), SnapshotPutFailures: s.putFailures.Load(),
		SnapshotsPruned: s.pruned.Load(), Audits: s.audits.Load(), AuditMismatches: s.auditMisses.Load()}
}
