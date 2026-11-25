package qtest

import (
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// cacheRounds numbers TestVerdictCacheKeepsNoRecords' explorations, so
// that no two in the process (-count) record the same sends.
// Process-wide.
var cacheRounds atomic.Int64

// The verdict cache holds every distinct run the test binary judged, so
// an entry keeps none of its run's records: a run whose doubles recorded
// a megabyte of sends leaves a hashed key and its report's positions
// behind, not the sends. It kept the steps and the key whole once, and a
// nightly's 500 seeds a test of raftkv (thousands of effect records a
// run, each with the message it sent) ran the runner out of memory.
// Not parallel: it measures the process's heap.
func TestVerdictCacheKeepsNoRecords(t *testing.T) {
	RequireQuint(t)
	const runs, size = 16, 1 << 20
	var round int64
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		ctx := qenv.ContextOf(c)
		// a send of its own in every seed's run: no two share a verdict
		sent := fmt.Sprintf("%d.%d.%d:%s", round, c.Choose("payload", 1<<30), c.Choose("payload", 1<<30), strings.Repeat("m", size))
		qobs.Effect(ctx, "rpc.peer.request", "Msgs", sent)
		qobs.Record(ctx, "deposit", nil, "amount", 7, "obs.balance", 8)
		return nil
	}}
	explore := func() (ran, cached int) {
		round = cacheRounds.Add(1) // an exploration's runs are its own too, under -count
		x := sys.Explore(t, ExploreConfig{Seeds: runs})
		x.Expect(t, All(Mismatch("deposit")))
		return len(x.Runs), x.Cached
	}
	explore() // the binding's checker and model loaded, outside the measure
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	ran, cached := explore() // GOTOGETHER_SEEDS may change how many
	if cached != 0 {
		t.Fatalf("%d runs judged from the cache: every run's sends are its own", cached)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	kept := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("the heap grew %d KB over %d runs", kept/1024, ran)
	if kept > int64(ran*size/4) {
		t.Errorf("the cache kept %d KB after %d runs that each sent %d KB: an entry holds its run's records", kept/1024, ran, size/1024)
	}
}
