package hegelprops_test

// The core's properties that Hegel checks. They live in a module of their
// own, so the core's own tests need no Hegel (and its users' modules none
// of its dependencies), and an unpublished one, which replaces the core
// and qhegel with this checkout: they test the core as it is, where
// qhegel's own tests run against the core qhegel requires.

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qload"
	"github.com/chucklehead-dev/gotogether/woven"
	"hegel.dev/go/hegel"
)

// Two read-modify-write increments of one counter, run concurrently.
func increments(s *qenv.Sim, st *qenv.Store, forUpdate bool) {
	for i := 0; i < 2; i++ {
		s.Spawn(fmt.Sprintf("inc%d", i), func(ctx context.Context) {
			_ = qenv.Retry(ctx, st, 5, func(tx qenv.Txn) error {
				var v any
				var err error
				if forUpdate {
					v, _, err = tx.GetForUpdate("n")
				} else {
					v, _ = tx.Get("n")
				}
				if err != nil {
					return err
				}
				n, _ := v.(int)
				tx.Put("n", n+1)
				return nil
			})
		})
	}
}

func runStore(t *testing.T, c qenv.Chooser, iso qenv.Isolation, setup func(*qenv.Sim, *qenv.Store)) *qenv.Store {
	t.Helper()
	s := qenv.New(c)
	defer s.Close()
	st := qenv.NewStore(s, "db", iso)
	st.Load("oncall/alice", true)
	st.Load("oncall/bob", true)
	setup(s, st)
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	return st
}

func lostUpdate(st *qenv.Store) bool { v, _ := st.Read("n"); return v != 2 }
func nobodyOnCall(st *qenv.Store) bool {
	a, _ := st.Read("oncall/alice")
	b, _ := st.Read("oncall/bob")
	return a == false && b == false
}

// Hegel finds the lost update under read committed and shrinks the
// schedule; the faithful (FOR UPDATE) version passes.
func TestHegelFindsLostUpdate(t *testing.T) {
	check := func(forUpdate bool) error {
		return hegel.Run(func(tc hegel.TestCase) {
			st := runStore(t, qhegel.Choices(tc), qenv.ReadCommitted, func(s *qenv.Sim, st *qenv.Store) { increments(s, st, forUpdate) })
			if lostUpdate(st) {
				tc.Errorf("lost update")
			}
		}, hegel.WithTestCases(100), hegel.WithDatabase(""))
	}
	if err := check(false); err == nil {
		t.Fatalf("want a lost update, got %v", err)
	}
	if err := check(true); err != nil {
		t.Fatal(err)
	}
}

// The scheduler's semantics, as a property over random programs: tasks
// sleep, yield, wait on Signals with timeouts and notify them, under a
// Hegel-chosen schedule, with the scheduler checking its own invariants.
//   - a Sleep lasts exactly its duration, a Yield no time;
//   - a Wait that reports a Notify ends at the time of a Notify of its
//     Signal made while it waited; one that reports none lasts its timeout,
//     and one without a timeout (0) always reports a Notify;
//   - the same choices replay the same run.
func TestSchedulerSemantics(t *testing.T) {
	type op struct {
		kind       int // 0 sleep, 1 yield, 2 wait, 3 notify
		sig        int
		d          time.Duration
		start, end time.Duration
		notified   bool
	}
	run := func(c qenv.Chooser, progs [][]op) ([][]op, map[int][]time.Duration) {
		s := qenv.New(c)
		s.Check = true
		defer s.Close()
		sigs := []*qenv.Signal{{}, {}}
		notifies := map[int][]time.Duration{}
		out := make([][]op, len(progs))
		running := len(progs) // programs not yet done (the closer's)
		for i, prog := range progs {
			out[i] = append([]op(nil), prog...)
			s.Spawn("task", func(ctx context.Context) {
				defer func() { running-- }()
				for j := range out[i] {
					o := &out[i][j]
					o.start = s.Elapsed()
					switch o.kind {
					case 0:
						qenv.Sleep(ctx, o.d)
					case 1:
						qenv.Yield(ctx)
					case 2:
						o.notified = sigs[o.sig].Wait(ctx, o.d)
					case 3:
						notifies[o.sig] = append(notifies[o.sig], s.Elapsed())
						sigs[o.sig].Notify()
					}
					o.end = s.Elapsed()
				}
			})
		}
		// a wait of 0 has no limit: while a program runs, the closer
		// notifies both signals every 100s (longer than a program's bounded
		// ops, at most 6 of 5s), so one that no program notifies ends at
		// the closer's
		s.Spawn("closer", func(ctx context.Context) {
			for running > 0 {
				qenv.Sleep(ctx, 100*time.Second)
				for i, sig := range sigs {
					notifies[i] = append(notifies[i], s.Elapsed())
					sig.Notify()
				}
			}
		})
		if err := s.Run(time.Hour); err != nil {
			t.Fatal(err)
		}
		return out, notifies
	}
	hegel.Test(t, func(ht *hegel.T) {
		progs := make([][]op, hegel.Draw(ht, hegel.Integers(1, 4)))
		for i := range progs {
			for range hegel.Draw(ht, hegel.Integers(1, 6)) {
				progs[i] = append(progs[i], op{kind: hegel.Draw(ht, hegel.Integers(0, 3)), sig: hegel.Draw(ht, hegel.Integers(0, 1)),
					d: time.Duration(hegel.Draw(ht, hegel.Integers(0, 5))) * time.Second})
			}
		}
		rec := &qenv.ChoiceLog{C: qhegel.Choices(ht)}
		got, notifies := run(rec, progs)
		for _, prog := range got {
			for _, o := range prog {
				switch {
				case o.kind == 0 && o.end-o.start != o.d:
					ht.Fatalf("Sleep(%v) lasted %v", o.d, o.end-o.start)
				case o.kind == 1 && o.end != o.start:
					ht.Fatalf("Yield took %v", o.end-o.start)
				case o.kind == 2 && !o.notified && (o.d == 0 || o.end-o.start != o.d):
					ht.Fatalf("Wait(%v) timed out after %v", o.d, o.end-o.start)
				case o.kind == 2 && o.notified:
					ok := false
					for _, n := range notifies[o.sig] {
						ok = ok || (n == o.end && n >= o.start)
					}
					if !ok {
						ht.Fatalf("Wait from %v reported a Notify at %v; notifies of signal %d: %v", o.start, o.end, o.sig, notifies[o.sig])
					}
				}
			}
		}
		again, _ := run(qenv.Replay(rec.Choices), progs)
		if !reflect.DeepEqual(got, again) {
			ht.Fatalf("replay differs:\n%v\n%v", got, again)
		}
	}, hegel.WithTestCases(200))
}

// What woven code calls, called directly: inside a simulation a real
// sync.RWMutex excludes as sync's does, under any schedule Hegel picks, with
// a waiting writer blocking new readers; a WaitGroup waits for tasks started
// by Go; outside a simulation the real methods run.
func TestWovenSyncInSimulation(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		s := qenv.New(qhegel.Choices(ht))
		s.Check = true
		defer s.Close()
		var mu sync.RWMutex
		var wg sync.WaitGroup
		readers, writers, done := 0, 0, 0
		for i := range 4 {
			s.Spawn("task", func(ctx context.Context) {
				for range 3 {
					if i%2 == 0 {
						woven.RWLock(&mu)
						writers++
						if writers != 1 || readers != 0 {
							ht.Fatalf("writer with %d writers, %d readers", writers, readers)
						}
						qenv.Sleep(ctx, time.Millisecond)
						writers--
						woven.RWUnlock(&mu)
					} else {
						woven.RLock(&mu)
						readers++
						if writers != 0 {
							ht.Fatalf("reader with a writer")
						}
						qenv.Sleep(ctx, time.Millisecond)
						readers--
						woven.RUnlock(&mu)
					}
				}
				woven.Add(&wg, 1)
				go woven.Go(func() { defer woven.Done(&wg); done++ })() //qenv:ok what weaving makes of a //qenv:go statement
			})
		}
		s.Spawn("waiter", func(ctx context.Context) {
			qenv.Sleep(ctx, time.Second)
			woven.Wait(&wg)
			if done != 4 {
				ht.Fatalf("Wait returned with %d of 4 done", done)
			}
		})
		if err := s.Run(time.Minute); err != nil {
			ht.Fatal(err)
		}
	}, hegel.WithTestCases(100))

	// outside: the real mutex
	var mu sync.Mutex
	woven.MutexLock(&mu)
	if mu.TryLock() {
		t.Fatal("outside a simulation, MutexLock did not take the real mutex")
	}
	woven.MutexUnlock(&mu)
}

func sim(lambda float64) qload.SimConfig {
	return qload.SimConfig{
		Server: qload.ServerConfig{Name: "server", ServiceRate: 10, QueueCap: 100},
		Client: qload.ClientConfig{Name: "client", Rate: qload.Constant(lambda), Timeout: 9 * time.Second, Retries: 3},
	}
}

// Hegel finds the smallest stressor that leaves the paper's example
// (9.5 req/s against 10) stuck, and none at 8 req/s that the system does
// not recover from within 15 minutes (from a full queue the chain predicts
// about 8).
func TestFindTrigger(t *testing.T) {
	if testing.Short() {
		t.Skip("searches hundreds of simulated runs")
	}
	space := qload.TriggerSpace{MaxExtraRate: 15, MaxDuration: 300 * time.Second, Seeds: 3}
	for _, base := range []float64{8, 9, 9.5} {
		ok, _, err := qload.Mostly(qload.OfSimulation(sim(base), base, 300*time.Second), qload.Trigger{}, 5)
		if err != nil {
			t.Fatalf("%.1f req/s recovery assessment: %v", base, err)
		}
		t.Logf("%.1f req/s, no trigger: recovers in most runs: %v", base, ok)
	}
	t0 := time.Now() //qenv:ok times the search, outside any simulation
	qhegel.FindTrigger(t, qload.OfSimulation(sim(9.5), 9.5, 300*time.Second), space, hegel.WithTestCases(40), hegel.WithSeed(1)).Expect(t, qload.SomeTrigger())
	t.Logf("searched in %v", time.Since(t0)) //qenv:ok
	// at 8 req/s the system always recovers
	qhegel.FindTrigger(t, qload.OfSimulation(sim(8), 8, 15*time.Minute), space, hegel.WithTestCases(30), hegel.WithSeed(1)).Expect(t, qload.NoTrigger())
}
