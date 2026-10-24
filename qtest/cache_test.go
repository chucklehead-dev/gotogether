package qtest

import (
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// overdraw deposits past the default LIMIT (100).
func overdraw(c qenv.Chooser) error {
	qobs.Record(qenv.ContextOf(c), "deposit", nil, "amount", 150, "obs.balance", 150)
	return nil
}

var instances atomic.Int64

// A verdict is of steps against one instance: the same steps are a
// violation with LIMIT 100 and conform with LIMIT 200 (or more), and the verdicts
// shared by every exploration (as a sweep's rows share them) keep them
// apart.
func TestVerdictCacheKeysTheInstance(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	cfg := ExploreConfig{Seeds: 1}
	System{Binding: "../validate/testdata/account.yaml", Run: overdraw}.Explore(t, cfg).Expect(t, Violates("underLimit"))
	// an instance of its own each time the test runs (-count): verdicts are
	// the test binary's, and a LIMIT checked by an earlier run is rightly
	// a cache hit
	limit := strconv.Itoa(200 + int(instances.Add(1)))
	x := System{Binding: "../validate/testdata/account.yaml", Run: overdraw, Opts: []Option{Constants(map[string]string{"LIMIT": limit})}}.
		Explore(t, cfg)
	x.Expect(t, Conforms())
	if x.Cached != 0 {
		t.Errorf("the LIMIT %s run took a verdict of another instance", limit)
	}
}

// A claim naming an invariant or action the model does not have fails at
// once: None(Violates("underLimt")) would otherwise hold for nothing.
func TestClaimNamesAreChecked(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	x := System{Binding: "../validate/testdata/account.yaml", Run: overdraw}.Explore(t, ExploreConfig{Seeds: 1})
	for _, c := range []Claim{None(Violates("underLimt")), Rejects("depositt")} {
		f := &failTB{TB: t}
		done := make(chan struct{})
		go func() { defer close(done); x.Expect(f, c) }() //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		<-done
		if len(f.errs) != 1 || !strings.Contains(f.errs[0], "misspelled") {
			t.Errorf("%v: %v", c, f.errs)
		}
	}
}
