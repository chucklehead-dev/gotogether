package qtest

import (
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A real run whose leader came 1s late (bound 3s) while the host stalled
// 1.5s is no verdict with HostStalls: not OK, not a FAIL, Stalled, and
// claims over runs every one of which is so fail, naming the stall: they
// judged nothing;
// without the option, or with a stall elsewhere, it violates the bound.
// An exploration refuses the option: its time is virtual.
func TestHostStalls(t *testing.T) {
	RequireQuint(t)
	const binding = "../validate/testdata/within.yaml"
	sec := func(f float64) time.Time {
		return time.Unix(1_700_000_000, 0).Add(time.Duration(f * float64(time.Second)))
	}
	var steps []qtrace.Step
	for i, s := range []struct {
		action string
		at     float64
	}{{"crash", 0}, {"heal", 1}, {"elect", 5}} {
		steps = append(steps, qtrace.Step{Action: s.action, Recorder: "n", Seq: uint64(i + 1), Time: sec(s.at)})
	}
	steps = append(steps, qtrace.Step{Action: "settled", Record: qtrace.Record{Settled: true}, Recorder: "n", Seq: 4, Time: sec(5)})
	name := "within 3s after healed: hasLeader"
	stall := qtrace.Stall{Start: sec(2), End: sec(3.5), Kind: "disk", Detail: "an fsync took 1.5s"}

	r := Validate(t, binding, steps, HostStalls(stall))
	if r.OK() || !r.Stalled() || len(r.HostStalls) != 1 || r.Report.Reason() != "no verdict (host stall)" {
		t.Errorf("with the stall: OK %v, Stalled %v, %q:\n%s", r.OK(), r.Stalled(), r.Report.Reason(), r.Report.Summary())
	}
	if Violates(name).Holds(r) {
		t.Errorf("an excused bound is violated")
	}
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { defer close(done); Runs(r).Expect(f, Conforms()) }() //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
	<-done
	if !f.failed || !strings.Contains(strings.Join(f.errs, "\n"), "every one of 1 runs has no verdict (host stall)") {
		t.Errorf("Conforms of a stalled run: %v", f.errs)
	}

	for _, x := range []struct {
		desc string
		opts []Option
	}{
		{"no stalls given", nil},
		{"a stall after the response", []Option{HostStalls(qtrace.Stall{Start: sec(6), End: sec(8), Kind: "sched"})}},
	} {
		r := Validate(t, binding, steps, x.opts...)
		if r.Stalled() || !Violates(name).Holds(r) {
			t.Errorf("%s: Stalled %v, want %s violated:\n%s", x.desc, r.Stalled(), name, r.Report.Summary())
		}
	}

	f = &failTB{TB: t}
	done = make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		System{Binding: binding, Rates: qenv.NoFaults, Opts: []Option{HostStalls(stall)}, Run: func(qenv.Chooser) error { return nil }}.
			Explore(f, ExploreConfig{Seeds: 1})
	}()
	<-done
	if !strings.Contains(strings.Join(f.errs, "\n"), "HostStalls") {
		t.Errorf("an exploration given HostStalls: %v", f.errs)
	}
}
