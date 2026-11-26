package qtest

import (
	"context"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// bulky records account.yaml's deposit and withdraw, each after effects
// of kilobytes apiece (account.yaml compares no effect): a run whose
// records are large, as raftkv's are (every double's send). A run a
// chooser picks withdraws more than the balance, which the model
// rejects. heap, if set, is called at each run's start, before it
// records anything.
func bulky(effects, size int, heap func()) func(c qenv.Chooser) error {
	return func(c qenv.Chooser) error {
		if heap != nil {
			heap()
		}
		// a run's steps its own (mostly), so its verdict is too: a run
		// judged afresh, whose report's steps carry their effects, not
		// one from the cache's, which carries none
		deposit := 40 + c.Choose("test.deposit", 60)
		withdraw := 20
		if c.Choose("test.overdraw", 8) == 0 {
			withdraw = deposit + 10
		}
		// in a simulation, where a step's effects are observed: each is
		// attached to the step after it, in its report too
		s := qenv.New(c)
		defer s.Close()
		s.Spawn("account", func(ctx context.Context) {
			for range effects {
				qobs.Effect(ctx, "mail.send", "body", strings.Repeat("x", size)) // a fresh allocation, the run's own
			}
			qobs.Record(ctx, "deposit", nil, "amount", deposit, "obs.balance", deposit)
			qobs.Record(ctx, "withdraw", nil, "amount", withdraw, "obs.balance", deposit-withdraw)
		})
		return s.Run(time.Second)
	}
}

// liveHeap is the heap after a collection: what the process holds.
func liveHeap() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// An exploration whose runs' records outgrow a batch judges them a batch
// at a time and lets go of each passing run's records once judged: the
// heap a run starts on stays bounded by the batch, where without
// batching (KeepRecords) it grows with every run made. The claims over
// the runs (Some, All, None) are judged over every run's verdict, and a
// failing run keeps its records.
//
// Not parallel: it measures the process's heap, which no other test
// shares while it runs (Go runs the parallel tests after the others).
func TestExploreBatchesBoundTheHeap(t *testing.T) {
	RequireQuint(t)
	const (
		seeds   = 64
		effects = 32
		size    = 16 << 10 // a run's records: half a megabyte
		perRun  = effects * size
	)
	explore := func(t *testing.T, opts ...Option) (*Exploration, []uint64) {
		var heaps []uint64
		base := liveHeap()
		sys := System{Binding: "../validate/testdata/account.yaml", Opts: opts, Run: bulky(effects, size, func() {
			h := liveHeap()
			heaps = append(heaps, max(h, base)-base)
		})}
		x := sys.Explore(t, ExploreConfig{Seeds: seeds, Parallel: 1})
		x.Expect(t, Some(Conforms()), Some(Rejects("withdraw")), None(Violates("nonNegative")))
		return x, heaps
	}
	// a batch of 8 runs' records
	batched, bh := explore(t, judgeEvery(8*(effects+2)))
	kept, kh := explore(t, judgeEvery(8*(effects+2)), KeepRecords())

	peak := slicesMax
	t.Logf("live heap at a run's start, beyond the exploration's: batched peak %d KB, last %d KB; kept peak %d KB, at run %d %d KB, last %d KB (a run's records %d KB)",
		peak(bh)>>10, bh[len(bh)-1]>>10, peak(kh)>>10, seeds/2, kh[seeds/2]>>10, kh[len(kh)-1]>>10, perRun>>10)
	// linear: every run's records, kept
	if last := kh[len(kh)-1]; last < (seeds-8)*perRun*3/4 {
		t.Errorf("kept: the heap was %d KB at the last run's start, less than three quarters of %d runs' records (%d KB): the test measures nothing", last>>10, seeds-8, (seeds-8)*perRun>>10)
	}
	if kh[len(kh)-1] < kh[seeds/2]*3/2 {
		t.Errorf("kept: the heap at the last run's start (%d KB) is not half again its size at run %d (%d KB): it does not grow with the runs", kh[len(kh)-1]>>10, seeds/2, kh[seeds/2]>>10)
	}

	// the verdicts are the same, every run's judged; records kept by the
	// failing runs, released by the passing ones
	var failing, released int
	for i, r := range batched.Runs {
		k := kept.Runs[i]
		if r.Report.OK() != k.Report.OK() || r.origin != k.origin {
			t.Errorf("run %d: batched %v (%s), kept %v (%s)", i, r.Report.OK(), batched.nameOf(r), k.Report.OK(), kept.nameOf(k))
		}
		if len(k.Steps) == 0 || len(k.Recorded) == 0 {
			t.Errorf("run %d: KeepRecords kept no records", i)
		}
		switch {
		case !r.Report.OK():
			failing++
			if r.released || len(r.Recorded) != effects+2 || len(r.Steps) == 0 {
				t.Errorf("failing run %d: released %v, %d records recorded, %d judged: a failing run keeps its records", i, r.released, len(r.Recorded), len(r.Steps))
			}
		case r.released:
			released++
			if r.Steps != nil || r.Recorded != nil {
				t.Errorf("released run %d keeps %d steps, %d records", i, len(r.Steps), len(r.Recorded))
			}
		}
	}
	// bounded: two batches' runs (the one judged, the next made) and the
	// failing runs', which keep their records
	if p := peak(bh); p > uint64(16+failing)*perRun {
		t.Errorf("batched: the heap reached %d KB at a run's start, more than %d runs' records (%d KB: two batches, and %d failing runs): the judged runs' records are kept",
			p>>10, 16+failing, (16+failing)*perRun>>10, failing)
	}
	if failing == 0 || released < seeds-failing-16 {
		t.Errorf("%d failing runs, %d of %d passing released: the batches did not release", failing, released, seeds-failing)
	}
}

// slicesMax is the largest of hs (0 for none).
func slicesMax(hs []uint64) uint64 {
	var m uint64
	for _, h := range hs {
		m = max(m, h)
	}
	return m
}

// A passing run whose records were released, shown by a claim that fails
// of it, is made again from its choices for its step log: the claim's
// report reads as it does of a run that kept its records.
func TestExploreBatchesRestoreAShownRun(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	f := &failTB{TB: t}
	sys := System{Binding: "../validate/testdata/account.yaml", Opts: []Option{judgeEvery(4)}, Run: bulky(2, 16, nil)}
	var x *Exploration
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		x = sys.Explore(f, ExploreConfig{Seeds: 16, Parallel: 2})
		x.Expect(f, None(Conforms()))
	}()
	<-done
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], " in no run; ") || strings.Contains(f.errs[0], "could not be made again") {
		t.Fatalf("errors %q", f.errs)
	}
	var shown *Run
	for _, r := range x.Runs {
		if r.Report.OK() {
			shown = r
			break
		}
	}
	if shown == nil || shown.released || len(shown.Recorded) != 4 || len(shown.Steps) == 0 {
		t.Fatalf("the shown run was not restored: %+v", shown)
	}
	released := 0
	for _, r := range x.Runs {
		if r.released {
			released++
		}
	}
	if released == 0 {
		t.Errorf("no run released: the test shows nothing")
	}
}

// Vacuity is judged over the runs whose records were released: their
// antecedents' witnesses were looked for before. One run of twelve
// passes the gate, in a batch of its own, released.
func TestExploreBatchesWitnessAntecedents(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	gate := func(c qenv.Chooser) error {
		ctx := qenv.ContextOf(c)
		qobs.Record(ctx, "open", nil)
		if c.Choose("test.pass", 12) == 0 {
			qobs.Record(ctx, "pass", nil)
		}
		return nil
	}
	x := System{Binding: "testdata/gate.yaml", Opts: []Option{judgeEvery(1)}, Run: gate}.Explore(t, ExploreConfig{Seeds: 24})
	witnesses := 0
	for _, r := range x.Runs {
		if slices.ContainsFunc(r.Choices, func(c qenv.Choice) bool { return c.Kind == "test.pass" && c.Value == 0 }) {
			witnesses++
			if !r.released {
				t.Errorf("%s, a witness, kept its records: the test shows nothing", x.nameOf(r))
			}
		}
	}
	if witnesses == 0 {
		t.Fatal("no run passed the gate: the test shows nothing")
	}
	x.Expect(t, Conforms(), NotVacuous())
}

// A steered exploration steers from its runs' features, which a released
// run keeps: its steered runs are those of one that keeps every record.
func TestExploreBatchesSteerAlike(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	// each its test's first exploration: steering draws from its id
	explore := func(name string, opts ...Option) (x *Exploration) {
		t.Run(name, func(t *testing.T) {
			x = System{Binding: "../validate/testdata/account.yaml", Opts: opts, Run: bulky(2, 16, nil)}.Explore(t, ExploreConfig{Seeds: 16, Steer: 8})
		})
		return x
	}
	batched, kept := explore("batched", judgeEvery(4)), explore("kept", KeepRecords())
	released := 0
	for i, r := range batched.Runs {
		if r.released {
			released++
		}
		k := kept.Runs[i]
		if batched.nameOf(r) != kept.nameOf(k) || !reflect.DeepEqual(r.Choices, k.Choices) {
			t.Errorf("run %d: batched %s (%d choices), kept %s (%d choices)", i, batched.nameOf(r), len(r.Choices), kept.nameOf(k), len(k.Choices))
		}
	}
	if released == 0 {
		t.Error("no run released: the test shows nothing")
	}
}
