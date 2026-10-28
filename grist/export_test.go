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
