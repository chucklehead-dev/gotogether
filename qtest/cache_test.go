package qtest

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/validate"
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

// An eventual val is judged only in a trace whose last record is a
// settle: a claim on one that no run judged fails with the fix, where
// None(Violates(...)) of an emptied account never settled held for
// nothing. The report says why it was not judged.
func TestClaimOnAnUnjudgedEventualVal(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	emptied := func(c qenv.Chooser) error {
		ctx := qenv.ContextOf(c)
		qobs.Record(ctx, "deposit", nil, "amount", 5, "obs.balance", 5)
		qobs.Record(ctx, "withdraw", nil, "amount", 5, "obs.balance", 0)
		return nil // and never settles
	}
	x := System{Binding: "../validate/testdata/funds.binding.yaml", Run: emptied}.Explore(t, ExploreConfig{Seeds: 1})
	if !strings.Contains(fmt.Sprint(x.Runs[0].Report.Actors[0].Issues), "eventual vals not judged (funded)") {
		t.Errorf("the report does not say funded was not judged: %v", x.Runs[0].Report.Actors[0].Issues)
	}
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { defer close(done); x.Expect(f, None(Violates(validate.EventuallyName("funded")))) }() //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
	<-done
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "which no run judged") {
		t.Errorf("claim on an unjudged eventual val: %v", f.errs)
	}
}

// Conformance claimed of runs none of which settled, with a binding that
// has eventual vals, fails with the fix: it held of a store that never
// converged. Claiming whether runs settle states what was judged.
func TestConformsWithEventualValsUnjudged(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	emptied := func(c qenv.Chooser) error {
		ctx := qenv.ContextOf(c)
		qobs.Record(ctx, "deposit", nil, "amount", 5, "obs.balance", 5)
		qobs.Record(ctx, "withdraw", nil, "amount", 5, "obs.balance", 0)
		return nil
	}
	x := System{Binding: "../validate/testdata/funds.binding.yaml", Run: emptied}.Explore(t, ExploreConfig{Seeds: 1})
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { defer close(done); x.Expect(f, Conforms()) }() //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
	<-done
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "judged in none of 1 runs") {
		t.Errorf("Conforms of runs that never settled: %v", f.errs)
	}
	x.Expect(t, Conforms(), None(Settles()))
}

// A claim that no run shows something holds only of runs judged: two
// concurrent withdrawals of 50 from 60 that both succeed, linearized
// within a budget of one order, have no verdict, and None(Mismatch(""))
// once held of all three (the full search finds the mismatch).
func TestNoneOverRunsWithNoVerdict(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	race := func(c qenv.Chooser) error {
		ctx := qenv.ContextOf(c)
		qobs.Record(ctx, "deposit", nil, "amount", 60, "obs.balance", 60)
		a := qobs.Invoke(ctx, "withdraw", "amount", 50)
		b := qobs.Invoke(ctx, "withdraw", "amount", 50)
		d := qobs.Invoke(ctx, "deposit", "amount", 1)
		d.Done(nil, "obs.balance", 61)
		a.Done(nil, "obs.balance", 10)
		b.Done(nil, "obs.balance", -40)
		return nil
	}
	sys := System{Binding: "../validate/testdata/account.yaml", Run: race, Opts: []Option{LinearizeBudget(1)}}
	x := sys.Explore(t, ExploreConfig{Seeds: 2})
	for _, c := range []Claim{None(Mismatch("")), None(Rejects("withdraw"))} {
		f := &failTB{TB: t}
		done := make(chan struct{})
		go func() { defer close(done); x.Expect(f, c) }() //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		<-done
		if len(f.errs) != 1 || !strings.Contains(f.errs[0], "have no verdict") {
			t.Errorf("%v over runs with no verdict: %v", c, f.errs)
		}
	}
	sys.Opts = []Option{Linearize()}
	sys.Explore(t, ExploreConfig{Seeds: 1}).Expect(t, Mismatch(""))
}

// A claim's invariant names are the binding's: with no invariant listed
// and no run settled (so no report names one), a misspelled name once
// went unchecked and None(Violates(...)) held for nothing.
func TestClaimNamesAreTheBindings(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	emptied := func(c qenv.Chooser) error {
		ctx := qenv.ContextOf(c)
		qobs.Record(ctx, "deposit", nil, "amount", 5, "obs.balance", 5)
		return nil // and never settles
	}
	x := System{Binding: "../validate/testdata/funds.binding.yaml", Run: emptied}.Explore(t, ExploreConfig{Seeds: 1})
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { defer close(done); x.Expect(f, None(Violates("fundedd"))) }() //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
	<-done
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "misspelled") {
		t.Errorf("a name the binding does not have: %v", f.errs)
	}
}
