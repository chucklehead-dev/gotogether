package grist

import (
	"context"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/internal/readcfg"
)

// NewMainServer is serve's http.Server, for its bounds' test.
var NewMainServer = newMainServer

// SetMemoryLimit is serve's GOMEMLIMIT from a cgroup's memory.max file.
var SetMemoryLimit = setMemoryLimit

// SetLiveBound shortens /livez's bound for a test, returning the restore.
func SetLiveBound(d time.Duration) func() {
	old := liveBound
	liveBound = d
	return func() { liveBound = old }
}

// DrainShares is d's phases (defaults applied) and their shares of its
// grace, and the startup line's account of them.
func DrainShares(d Drain, orchestrator bool) (Drain, string) {
	d = d.withDefaults(orchestrator)
	return d.budget(), d.line()
}

// MainArgs is Main's commands on args, for the commands' tests.
var MainArgs = mainArgs

// UsageError is a command line Main cannot run.
type UsageError = usageError

// QueryArgs is the API's reading of a query's URL parameters.
var QueryArgs = queryArgs

// The bounds the tests check.
const (
	MaxIDBytes      = maxIDBytes
	ShutdownTimeout = shutdownTimeout
	DrainDelay      = DefaultDrainDelay
)

// RelayFor makes r upcast its rows' old events to a's newest, as App.Start
// does for its relay.
func RelayFor(r *Relay, a *App) { r.app.Store(a) }

// SnapshotSum is a snapshot's checksum, for a test that writes one: the
// snapshot's fields and its last event's identity (EventID).
var SnapshotSum = snapshotSum

// EventID is a stored event's identity, a snapshot's and the memo's
// anchor.
var EventID = eventID

// SnapshotEvery is ctx with reads' snapshots every n events, in place of
// the App's (gristsim's Config.SnapshotEvery's override).
func SnapshotEvery(ctx context.Context, n int) context.Context {
	return readcfg.With(ctx, readcfg.Settings{Every: n, HasEvery: true})
}

// SnapshotAudit is ctx with one in n reads from a snapshot or the memo
// audited, in place of the App's.
func SnapshotAudit(ctx context.Context, n int) context.Context {
	return readcfg.With(ctx, readcfg.Settings{Audit: n, HasAudit: true})
}

// NoMemo is ctx with the memo off, for a test of snapshots' own reads.
func NoMemo(ctx context.Context) context.Context {
	return readcfg.With(ctx, readcfg.Settings{Memo: 0, HasMemo: true})
}

// ExitStatus is a helper binary's exit status, as Main passes it on.
type ExitStatus = exitStatus
