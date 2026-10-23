package qenv_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// The native runtime on the wall clock: the same doubles and processes as
// on qenv's scheduler, on real goroutines and real (short) time. In gosim
// the same code runs on simulated time (examples/gosim).

// Read-modify-write increments with FOR UPDATE never lose an update, on
// real goroutines too: tasks still run one at a time between seams.
func TestNativeIncrements(t *testing.T) {
	for i := 0; i < 20; i++ {
		s := qenv.NewNative(qenv.NewSeeded(uint64(i)), nil)
		st := qenv.NewStore(s, "db", qenv.ReadCommitted)
		increments(s, st, true)
		if err := s.Run(50 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		v, _ := st.Read("n")
		s.Close()
		if v != 2 {
			t.Fatalf("run %d: n=%v, want 2", i, v)
		}
	}
}

// A crashed worker's transaction is released and its delivery redelivered
// to the restarted process; the crashing task stops at once.
func TestNativeCrashReleasesAndRedelivers(t *testing.T) {
	s := qenv.NewNative(qenv.Nominal{}, nil)
	st := qenv.NewStore(s, "db", qenv.ReadCommitted)
	q := qenv.NewQueue(s, "jobs", nil)
	crashNext := true
	s.OnCrash(func(p *qenv.Process) { p.Restart() })
	var p *qenv.Process
	ranOn := false
	p = s.Process("worker", false, func(ctx context.Context) {
		for {
			d := q.Receive(ctx)
			tx := st.Begin(ctx)
			if _, _, err := tx.GetForUpdate("job"); err != nil {
				t.Error(err)
			}
			tx.Put("job", d.Body)
			if crashNext {
				crashNext = false
				p.Crash()
				ranOn = true
			}
			if err := tx.Commit(); err != nil {
				t.Error(err)
			}
			d.Ack(ctx)
		}
	})
	s.Spawn("client", func(ctx context.Context) { q.Publish(ctx, "j1") })
	if err := s.Run(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	v, _ := st.Read("job")
	redelivered, inc := q.Redelivered, p.Incarnation
	s.Close()
	if ranOn || v != "j1" || redelivered != 1 || inc != 2 {
		t.Fatalf("ran on=%v job=%v redelivered=%d incarnation=%d", ranOn, v, redelivered, inc)
	}
}

// A lost response is a timeout, after the request took effect, and the
// caller waited the timeout out.
func TestNativeLostResponse(t *testing.T) {
	applied := 0
	var got error
	var waited time.Duration
	s := qenv.NewNative(&forced{faults: map[string]int{"fault.rpc.pay.response": 1}}, nil)
	svc := qenv.NewService(s, "pay", nil, func(ctx context.Context, n int) (int, error) { applied += n; return applied, nil })
	s.Spawn("client", func(ctx context.Context) {
		start := qenv.Now(ctx)
		_, got = svc.Call(ctx, 5, 30*time.Millisecond)
		waited = qenv.Now(ctx).Sub(start)
	})
	if err := s.Run(100 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if !errors.Is(got, qenv.ErrTimeout) || applied != 5 || waited < 30*time.Millisecond {
		t.Fatalf("err=%v applied=%d waited=%v", got, applied, waited)
	}
}

// Predicate waits and Signals wake on the state they wait for.
func TestNativeWaits(t *testing.T) {
	s := qenv.NewNative(qenv.Nominal{}, nil)
	var sig qenv.Signal
	ready, woke, timedOut := false, false, true
	s.Spawn("waiter", func(ctx context.Context) {
		qenv.Wait(ctx, func() bool { return ready })
		woke = true
	})
	s.Spawn("signalled", func(ctx context.Context) {
		timedOut = !sig.Wait(ctx, time.Second)
	})
	s.Spawn("setter", func(ctx context.Context) {
		qenv.Sleep(ctx, 10*time.Millisecond)
		ready = true
		sig.Notify()
	})
	if err := s.Run(100 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if !woke || timedOut {
		t.Fatalf("predicate waiter woke=%v; signal timed out=%v", woke, timedOut)
	}
}

// A task crashing its own process stops in Crash.
func TestNativeSelfCrash(t *testing.T) {
	s := qenv.NewNative(qenv.Nominal{}, nil)
	after := false
	s.Process("suicidal", false, func(ctx context.Context) {
		qenv.ProcessOf(ctx).Crash()
		after = true
	})
	if err := s.Run(20 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if after {
		t.Fatal("the task ran on after crashing its own process")
	}
}

// ContextOf is the context a chooser carries: where a run elsewhere
// records.
func TestContextOf(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, 1)
	if qenv.ContextOf(qenv.WithContext(qenv.Nominal{}, ctx)).Value(key{}) != 1 {
		t.Fatal("WithContext's context is lost")
	}
	if qenv.ContextOf(qenv.Nominal{}) == nil {
		t.Fatal("no context for a plain chooser")
	}
}

// Entropy reaches through the wrappers: a Recorder records each chunk and
// Replay hands the same chunks back, between the ordinary choices.
func TestEntropyRecordsAndReplays(t *testing.T) {
	rec := &qenv.ChoiceLog{C: qenv.NewSeeded(5)}
	c := qenv.WithContext(rec, context.Background())
	e := qenv.EntropyOf(c)
	if e == nil {
		t.Fatal("no entropy through WithContext and Recorder")
	}
	a := e(8)
	k := c.Choose("fault.x", 2)
	b := e(8)
	r := qenv.Replay(rec.Choices)
	re := qenv.EntropyOf(r)
	if !reflect.DeepEqual(re(8), a) || r.Choose("fault.x", 2) != k || !reflect.DeepEqual(re(8), b) {
		t.Fatalf("replay differs from %v", rec.Choices)
	}
	if qenv.EntropyOf(qenv.Nominal{}) != nil {
		t.Fatal("Nominal has no entropy")
	}
}

// Without a Platform the native runtime runs in real time, and Mono is
// the host's monotonic clock: a sleep's interval, not wall arithmetic.
func TestNativeMono(t *testing.T) {
	s := qenv.NewNative(qenv.NewSeeded(1), nil)
	var took time.Duration
	s.Spawn("sleeper", func(ctx context.Context) {
		m := qenv.Mono(ctx)
		qenv.Sleep(ctx, 50*time.Millisecond)
		took = qenv.Since(ctx, m)
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if took < 50*time.Millisecond || took > time.Second {
		t.Errorf("Mono measured %v across a 50ms sleep", took)
	}
}

// The fault models added after the native runtime run on it too, each
// seen doing what it does: a wall clock skewed and jumping, a drifting
// clock's sleep lasting less true time, a cut the partition nemesis made
// failing a call across it, and a crash taking the unsynced writes of
// its machine's disk. Real time, so each run is a few milliseconds and the
// nemesis is considered every millisecond.
func TestNativeFaultModels(t *testing.T) {
	native := func(seed uint64, rates qenv.Rates) *qenv.Sim {
		c := qenv.NewSeeded(seed)
		c.Rates = rates
		s := qenv.NewNative(c, nil)
		s.NemesisEvery = time.Millisecond
		return s
	}
	t.Run("skew and jumps", func(t *testing.T) {
		skewed, jumped := 0, 0
		for seed := uint64(1); seed <= 10; seed++ {
			s := native(seed, qenv.NoFaults.Set("clock.skew", 0.5).Set("nemesis", 0.5))
			s.Clocks = qenv.Clocks{Skew: time.Second, Jump: 10 * time.Second}
			node := s.Process("node", false, func(ctx context.Context) {
				for range 20 {
					qenv.Sleep(ctx, time.Millisecond)
				}
			})
			if err := s.Run(200 * time.Millisecond); err != nil {
				t.Fatal(err)
			}
			o := node.ClockOffset() // the skew (±1 s) plus jumps of 10 s
			if (o % (10 * time.Second)).Abs() == time.Second {
				skewed++
			}
			if o.Abs() >= 9*time.Second {
				jumped++
			}
			s.Close()
		}
		if skewed == 0 || jumped == 0 {
			t.Errorf("over 10 runs: %d clocks skewed, %d jumped: want both", skewed, jumped)
		}
	})
	t.Run("drift", func(t *testing.T) {
		fast := false
		for seed := uint64(1); seed <= 10 && !fast; seed++ {
			s := native(seed, qenv.NoFaults.Set("clock.drift", 1))
			s.Clocks = qenv.Clocks{Drift: 0.5}
			var took time.Duration
			s.Process("node", false, func(ctx context.Context) {
				start := time.Now() //qenv:ok true time, to see the drift
				qenv.Sleep(ctx, 30*time.Millisecond)
				took = time.Since(start) //qenv:ok true time, to see the drift
			})
			if err := s.Run(200 * time.Millisecond); err != nil {
				t.Fatal(err)
			}
			fast = took < 25*time.Millisecond // a fast clock's 30 ms are 20 ms; a slow machine only lengthens it
			s.Close()
		}
		if !fast {
			t.Error("no 30 ms sleep on a clock 50% fast lasted under 25 ms of true time")
		}
	})
	t.Run("partitions", func(t *testing.T) {
		across := 0
		for seed := uint64(1); seed <= 10; seed++ {
			s := native(seed, qenv.NoFaults.Set("nemesis", 0.2))
			s.Net().CutLength = 3 * time.Millisecond
			server := s.Process("server", false, func(context.Context) {})
			svc := qenv.NewService(s, "echo", server, func(ctx context.Context, n int) (int, error) { return n, nil })
			var node *qenv.Process
			node = s.Process("node", false, func(ctx context.Context) {
				for i := range 20 {
					cut := s.Net().Partitioned(node, server)
					if _, err := svc.Call(ctx, i, time.Millisecond); errors.Is(err, qenv.ErrTimeout) && cut {
						across++
					}
					qenv.Sleep(ctx, time.Millisecond)
				}
			})
			s.Net().PartitionNemesis(node, server)
			if err := s.Run(200 * time.Millisecond); err != nil {
				t.Fatal(err)
			}
			s.Close()
		}
		if across == 0 {
			t.Error("over 10 runs, no call made across a cut timed out")
		}
	})
	t.Run("disk", func(t *testing.T) {
		for _, rate := range []float64{0, 1} {
			s := native(1, qenv.NoFaults.Set("disk", rate))
			var disk *qenv.Disk
			var node *qenv.Process
			node = s.Process("node", false, func(ctx context.Context) {
				f := disk.Create("log")
				_, _ = f.Write([]byte("synced\n"))
				_ = f.Sync()
				_, _ = f.Write([]byte("unsynced\n"))
				node.Crash()
			})
			disk = qenv.NewDisk(s, "node", node)
			if err := s.Run(200 * time.Millisecond); err != nil {
				t.Fatal(err)
			}
			data, _ := disk.ReadFile("log")
			s.Close()
			kept := strings.Contains(string(data), "unsynced")
			if !node.Down() || !strings.HasPrefix(string(data), "synced\n") || kept != (rate == 0) {
				t.Errorf("disk faults at rate %v: down %v, the file %q", rate, node.Down(), data)
			}
		}
	})
}
