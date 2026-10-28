package qeval

import (
	"runtime/debug"
	"sync"
)

// A search allocates its states and labels faster than it drops them
// (most of what it builds is live until it ends), so the collector's
// default pace, a cycle each time the heap doubles, spends a third of a
// bank explore collecting: at 400 the explore was 32% faster alone (the
// eleventh review, I39). The percent is process-wide, so searches running
// at once share one raise: the first sets it, the last puts back what was
// there. A percent already set higher, or off (GOGC=off), is left alone.
const searchGCPercent = 400

// gcRaise is the searches' shared raise, process-wide: how many run, and
// the percent before the first
var gcRaise struct {
	sync.Mutex
	n      int
	before int
	raised bool
}

// raiseGC raises the GC percent for a search; the function it returns
// ends the search's part in it.
func raiseGC() (restore func()) {
	gcRaise.Lock()
	defer gcRaise.Unlock()
	if gcRaise.n == 0 {
		// SetGCPercent is the only way to read it: set it, and put it back
		// if it was already as high or off
		before := debug.SetGCPercent(searchGCPercent)
		gcRaise.before, gcRaise.raised = before, true
		if before < 0 || before >= searchGCPercent {
			debug.SetGCPercent(before)
			gcRaise.raised = false
		}
	}
	gcRaise.n++
	return func() {
		gcRaise.Lock()
		defer gcRaise.Unlock()
		gcRaise.n--
		if gcRaise.n == 0 && gcRaise.raised {
			debug.SetGCPercent(gcRaise.before)
			gcRaise.raised = false
		}
	}
}
