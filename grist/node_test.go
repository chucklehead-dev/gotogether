package grist_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// A tenant created after the node started is relayed and handled: the
// node's watcher waits on the store's tenant feed and starts the
// tenant's relay and pollers. Without it the tenant's commits were never
// recorded (the relay stayed behind by every one) and its triggers never
// handled.
func TestNodeStartsNewTenants(t *testing.T) {
	a, added, _ := procApp(nil)
	sink := &qobs.MemorySink{}
	s := qenv.New(qenv.WithContext(qenv.NewSeeded(1), qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))))
	defer s.Close()
	s.Check = true
	st := simstore.New(qenv.NewStore(s, "procs", qenv.Serializable))
	ctx := s.Context()
	st.CreateTenant(ctx, "t1")
	relay, err := grist.NewRelay(st)
	if err != nil {
		t.Fatal(err)
	}
	s.Process("node", true, func(ctx context.Context) {
		if err := a.Start(ctx, st, grist.WithRelay(relay)); err != nil {
			t.Error(err)
		}
	})
	s.Spawn("later", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Second) // the node is up, with t1 alone
		st.CreateTenant(ctx, "t2")
		st.Append(ctx, "t2", store.Batch{Events: []grist.Event{added.Of(Added{By: 2})}})
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	summed, _, err := st.Read(ctx, "t2", grist.Selector{Types: []string{"summed"}}, 0)
	if err != nil || len(summed) != 1 {
		t.Errorf("t2's summed events: %v (%v), want the sum processor's one: its poller started", summed, err)
	}
	if n, err := relay.Behind(ctx); err != nil || n != 0 {
		t.Errorf("the relay is behind by %d (%v), want 0: t2's commits recorded", n, err)
	}
	found := false
	for _, step := range sink.Steps() {
		found = found || step.Actor == "t2" && step.Action == "sum" && step.Recorder == grist.RecorderID("procs", "t2")
	}
	if !found {
		t.Error("no relayed sum step of t2")
	}
}

// slowFirst is a store whose first append waits 10 ms before it begins:
// a command that decided first commits after one that decided later.
type slowFirst struct {
	*simstore.Store
	slept bool
}

func (s *slowFirst) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	if !s.slept {
		s.slept = true
		qenv.Sleep(ctx, 10*time.Millisecond)
	}
	return s.Store.Append(ctx, t, b)
}

// A relayed step's time is its commit's, taken under the tenant's lock
// (store.StepRow.Time), so the relayed recorder's seq order (commit
// order) is time order too. It was the decision's (the row encoded
// before the append), so a command that decided first and committed
// second carried the earlier time at the later seq.
func TestRelayedTimesInCommitOrder(t *testing.T) {
	_, _, cmd := app(false)
	s := qenv.New(qenv.NewSeeded(1))
	defer s.Close()
	st := &slowFirst{Store: simstore.New(qenv.NewStore(s, "counters", qenv.ReadCommitted))}
	st.CreateTenant(s.Context(), "t1")
	s.Spawn("first", func(ctx context.Context) {
		cmd.Execute(ctx, st, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "x", Name: "a"})
	})
	s.Spawn("second", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Millisecond)
		cmd.Execute(ctx, st, grist.Envelope{ID: "c2", Tenant: "t1"}, Create{Counter: "y", Name: "b"})
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	sink := &qobs.MemorySink{}
	relay, _ := grist.NewRelay(st)
	if err := relay.Flush(qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))); err != nil {
		t.Fatal(err)
	}
	steps := sink.Steps()
	if len(steps) != 2 || steps[0].Args["id"] != "c2" {
		t.Fatalf("relayed %v: want c2's commit first, then c1's", steps)
	}
	if !steps[1].Time.After(steps[0].Time) {
		t.Errorf("c2 committed first at %v, c1 second at %v: want seq order to be time order", steps[0].Time, steps[1].Time)
	}
}

// A step row of a version this grist does not read stops its tenant's
// relay with an error naming the fix (Relay.Err), rather than a retry
// forever; a run that cannot record its commits says so.
func TestRelayRefusesUnknownVersion(t *testing.T) {
	a, _, _ := app(false)
	s := qenv.New(qenv.NewSeeded(1))
	defer s.Close()
	st := simstore.New(qenv.NewStore(s, "counters", qenv.Serializable))
	ctx := s.Context()
	st.CreateTenant(ctx, "t1")
	st.Append(ctx, "t1", store.Batch{Step: []byte(`{"v":2,"steps":[]}`)})
	relay, _ := grist.NewRelay(st)
	s.Process("node", true, func(ctx context.Context) {
		if err := a.Start(ctx, st, grist.WithRelay(relay)); err != nil {
			t.Error(err)
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	err := relay.Err()
	if err == nil || !strings.Contains(err.Error(), "version 2") || !strings.Contains(err.Error(), "upgrade") || strings.Contains(err.Error(), "retrying") {
		t.Errorf("a row of version 2: relay error %v, want it stopped, naming the version and the fix", err)
	}
	if err := relay.Flush(context.Background()); err == nil {
		t.Error("Flush of a stopped relay: no error")
	}
}

// Start takes its relay as an option, and one of another store is its
// error, not ignored; a store with no tenant feed is refused.
func TestStartOptions(t *testing.T) {
	a, _, _ := app(false)
	other, _ := grist.NewRelay(newStore(t))
	if err := a.Start(context.Background(), newStore(t), grist.WithRelay(other)); err == nil || !strings.Contains(err.Error(), "WithRelay") {
		t.Errorf("Start with another store's relay: %v, want its error", err)
	}
	if err := a.Start(context.Background(), noFeed{newStore(t)}); err == nil || !strings.Contains(err.Error(), "TenantFeed") {
		t.Errorf("Start on a store without a tenant feed: %v, want its error", err)
	}
}

// noFeed is a store with a step log and no tenant feed.
type noFeed struct{ *memstore.Store }

func (noFeed) WaitTenants() {} // hides memstore's: not a store.TenantFeed

// Flush beside a node relaying with the same Relay records each row once,
// in order: the relay's progress is claimed and recorded under one lock
// that no seam is reached under (goroutines here, outside a simulation).
func TestFlushBesideANode(t *testing.T) {
	app, _, create := app(false)
	s := newStore(t)
	sink := &qobs.MemorySink{}
	ctx, cancel := context.WithCancel(qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink)))
	defer cancel()
	relay, _ := grist.NewRelay(s)
	if err := app.Start(ctx, s, grist.WithRelay(relay)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			for i := range 25 {
				if w%2 == 0 {
					create.Execute(ctx, s, grist.Envelope{ID: fmt.Sprintf("c%d-%d", w, i), Tenant: "t1"}, Create{Counter: fmt.Sprint(w, i), Name: fmt.Sprint(w, i)})
				} else if err := relay.Flush(ctx); err != nil {
					t.Error(err)
				}
				runtime.Gosched()
			}
		})
	}
	wg.Wait()
	if err := relay.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	seen := map[uint64]int{}
	for _, st := range sink.Steps() {
		if st.Recorder == grist.RecorderID("counters", "t1") {
			seen[st.Seq]++
		}
	}
	if len(seen) != 50 {
		t.Errorf("%d relayed seqs, want 50 (one per commit)", len(seen))
	}
	for seq, n := range seen {
		if n != 1 {
			t.Errorf("seq %d recorded %d times by one Relay", seq, n)
		}
	}
}

// A store name and a tenant name that join with a slash name no other
// pair's recorder.
func TestRecorderIDsDistinct(t *testing.T) {
	if grist.RecorderID("a/b", "c") == grist.RecorderID("a", "b/c") {
		t.Error("RecorderID(a/b, c) == RecorderID(a, b/c)")
	}
}

// Text no store holds alike is refused on every tier before it is
// stored: a NUL byte or invalid UTF-8 in a tenant, an id or a tag
// (PostgreSQL's 22021, where SQLite and memstore took them), and a
// slash in a tenant (kept for <tenant>/clients).
func TestTextChecked(t *testing.T) {
	_, _, cmd := app(false)
	s := newStore(t)
	for _, c := range []struct {
		env  grist.Envelope
		args Create
		want string
	}{
		{grist.Envelope{ID: "c1", Tenant: "t\x001"}, Create{Counter: "x", Name: "a"}, "NUL"},
		{grist.Envelope{ID: "c\xff", Tenant: "t1"}, Create{Counter: "x", Name: "a"}, "UTF-8"},
		{grist.Envelope{ID: "c3", Tenant: "t1/clients"}, Create{Counter: "x", Name: "a"}, "slash"},
		{grist.Envelope{ID: "c4", Tenant: "t1"}, Create{Counter: "x", Name: "a\x00"}, "NUL"},
	} {
		_, err := cmd.Execute(context.Background(), s, c.env, c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v %+v: %v, want refused naming %s", c.env, c.args, err, c.want)
		}
	}
	if h, _ := s.Heads(context.Background()); h["t1"] != 0 {
		t.Errorf("refused commands appended: head %d", h["t1"])
	}
	for _, bad := range []string{"", "a/b", "a\x00", "\xff"} {
		if grist.CheckTenant(bad) == nil {
			t.Errorf("CheckTenant(%q): nil", bad)
		}
	}
	if err := grist.CheckTenant("acme.example"); err != nil {
		t.Errorf("CheckTenant(acme.example): %v", err)
	}
}

// notCommitted fails every append before its commit (a lost race for the
// lock, every time).
type notCommitted struct{ *memstore.Store }

func (notCommitted) Append(context.Context, string, store.Batch) (store.Position, error) {
	return 0, store.NotCommitted(errors.New("lock_timeout: 55P03"))
}

// cancelling commits and then finds its context cancelled: its answer is
// lost.
type cancelling struct {
	*memstore.Store
	cancel func()
}

func (s cancelling) Append(ctx context.Context, t string, b store.Batch) (store.Position, error) {
	if _, err := s.Store.Append(ctx, t, b); err != nil {
		return 0, err
	}
	s.cancel()
	return 0, context.Canceled
}

// A command whose every transient error came before a commit
// (store.NotCommitted) knows nothing committed: ErrUnavailable, not
// ErrOutcomeUnknown. One whose context ended while its append committed
// does not know: ErrOutcomeUnknown (and context.Canceled), not a bare
// context.Canceled that would read as "nothing happened".
func TestOutcomeKnownBeforeCommit(t *testing.T) {
	_, _, cmd := app(false)
	if _, err := cmd.Execute(context.Background(), notCommitted{newStore(t)}, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "x", Name: "a"}); !errors.Is(err, grist.ErrUnavailable) {
		t.Errorf("every append failing before its commit: %v, want grist.ErrUnavailable", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := cancelling{Store: newStore(t), cancel: cancel}
	_, err := cmd.Execute(ctx, s, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "x", Name: "a"})
	if !errors.Is(err, grist.ErrOutcomeUnknown) || !errors.Is(err, context.Canceled) {
		t.Errorf("a commit whose context was cancelled: %v, want grist.ErrOutcomeUnknown wrapping context.Canceled", err)
	}
	if m, ok, _ := s.Marker(context.Background(), "t1", "c1"); !ok || m.Receipt.Pos != 1 {
		t.Errorf("the commit's marker: %+v %v, want it committed", m, ok)
	}
}

// failTB records a test's failures without failing it, and ends its
// goroutine at a Fatal.
type failTB struct {
	testing.TB
	errs []string
}

func (f *failTB) Helper()                         {}
func (f *failTB) Errorf(format string, a ...any)  { f.errs = append(f.errs, fmt.Sprintf(format, a...)) }
func (f *failTB) Fatalf(format string, a ...any)  { f.Errorf(format, a...); runtime.Goexit() }
func (f *failTB) Fatal(a ...any)                  { f.Errorf("%s", fmt.Sprint(a...)); runtime.Goexit() }
func (f *failTB) Logf(format string, args ...any) {}
func (f *failTB) Log(args ...any)                 {}
func (f *failTB) Cleanup(fn func())               { f.TB.Cleanup(fn) }
func (f *failTB) FailNow()                        { runtime.Goexit() }

// A relay recording with no recorder (its context has none, no run's
// goroutine, no global one) records as qobs.Record does: in a strict run
// (qtest's) each step is an escape that fails the run, not a row passed
// over in silence.
func TestRelayWithoutRecorderEscapes(t *testing.T) {
	_, _, cmd := app(false)
	s := newStore(t)
	cmd.Execute(context.Background(), s, grist.Envelope{ID: "c1", Tenant: "t1"}, Create{Counter: "x", Name: "a"})
	relay, _ := grist.NewRelay(s)
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatal ends the goroutine
		defer close(done)
		qtest.Record(f, "testdata/none.yaml", func(*qobs.Recorder) {
			flushed := make(chan error)
			go func() { flushed <- relay.Flush(context.Background()) }() //qenv:ok a goroutine outside the run, the escape under test
			if err := <-flushed; err != nil {
				t.Error(err)
			}
		})
	}()
	<-done
	all := strings.Join(f.errs, "\n")
	if !strings.Contains(all, "recorded from goroutines outside the scenario's run") {
		t.Errorf("a relay without a recorder in a strict run: errors %q, want its step escaped", f.errs)
	}
}
