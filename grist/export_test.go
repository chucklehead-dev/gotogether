package grist

import "time"

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

// SnapshotSum is a snapshot's checksum, for a test that writes one.
var SnapshotSum = snapshotSum

// ExitStatus is a helper binary's exit status, as Main passes it on.
type ExitStatus = exitStatus
