package qenv_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/woven"
)

// A bank of two accounts under one mutex, called as woven code calls it
// (woven.MutexLock and MutexUnlock are what qenv/weave and qweave make of
// k.mu.Lock() and k.mu.Unlock()): a transfer debits, sleeps inside the
// critical section (other tasks run, but none takes the lock), then
// credits. Its lock invariant is that the money is conserved, which is
// false inside every transfer and must be true again at its Unlock.
type bank struct {
	mu   sync.Mutex
	a, b int
}

const bankTotal = 100

// bankRun is one run of three transferring tasks, two transfers each, the
// amount a choice: the mutant "lost" drops the credit of a transfer of 3,
// leaving the money short at the Unlock; "restored" over-credits and
// takes the excess back before the Unlock (false inside, true at
// release). guard registers the invariant. It returns the run's lock
// invariant error, and where the failing unlock and the guard are.
type bankRun struct {
	err          *qenv.LockInvariantError
	runErr       error
	guardLine    int
	unlockLine   int
	stepAtUnlock map[string]int // each task's step at its last unlock
	checks       int            // how many times the invariant was evaluated
	unlocks      int
}

func runBank(t *testing.T, c qenv.Chooser, mutant string, guard bool) (r bankRun) {
	t.Helper()
	s := qenv.New(c)
	k := &bank{a: bankTotal}
	r.stepAtUnlock = map[string]int{}
	defer func() {
		if p := recover(); p != nil {
			e, ok := p.(*qenv.LockInvariantError)
			if !ok {
				panic(p)
			}
			r.err = e
		}
	}()
	defer s.Close()
	if guard {
		_, _, line, _ := runtime.Caller(0)
		r.guardLine = line + 2
		qenv.Guard(s.Context(), &k.mu, "conserved", func() bool { r.checks++; return k.a+k.b == bankTotal })
	}
	for i := range 3 {
		name := fmt.Sprintf("transfer-%d", i)
		s.Spawn(name, func(ctx context.Context) {
			for range 2 {
				n := s.Choose("amount", 5) + 1
				woven.MutexLock(&k.mu)
				k.a -= n
				qenv.Sleep(ctx, time.Millisecond)
				switch mutant {
				case "lost":
					if n != 3 {
						k.b += n
					}
				case "restored":
					k.b += n + 1
					qenv.Yield(ctx)
					k.b--
				default:
					k.b += n
				}
				r.stepAtUnlock[name] = qenv.Steps(s)
				r.unlocks++
				_, _, line, _ := runtime.Caller(0)
				r.unlockLine = line + 2
				woven.MutexUnlock(&k.mu)
			}
		})
	}
	r.runErr = s.Run(time.Second)
	return r
}

// The faithful bank holds its invariant at every unlock, though it is
// false inside every critical section; so does a mutant that breaks it
// inside and restores it before the release: the invariant holds at
// release, not inside.
func TestLockInvariantHoldsAtUnlock(t *testing.T) {
	for _, mutant := range []string{"", "restored"} {
		for seed := range uint64(10) {
			r := runBank(t, qenv.NewSeeded(seed), mutant, true)
			if r.err != nil || r.runErr != nil {
				t.Fatalf("%q seed %d: %v %v", mutant, seed, r.err, r.runErr)
			}
			if r.checks != 6 || r.unlocks != 6 {
				t.Fatalf("%q seed %d: %d checks of %d unlocks, want one at each of 6", mutant, seed, r.checks, r.unlocks)
			}
		}
	}
}

// A critical section that leaves the invariant false fails at the Unlock
// that published it, in the task that released it, at its scheduling
// step, with a message naming all of them and the fix; the same seed
// fails the same way. Unguarded, the same runs pass: the guard is what
// catches it.
func TestLockInvariantBrokenAtUnlock(t *testing.T) {
	caught := 0
	for seed := range uint64(20) {
		r := runBank(t, qenv.NewSeeded(seed), "lost", true)
		if r.runErr != nil {
			t.Fatalf("seed %d: %v", seed, r.runErr)
		}
		if unguarded := runBank(t, qenv.NewSeeded(seed), "lost", false); unguarded.err != nil || unguarded.runErr != nil {
			t.Fatalf("seed %d unguarded: %v %v", seed, unguarded.err, unguarded.runErr)
		}
		if r.err == nil {
			continue
		}
		caught++
		e := r.err
		want := fmt.Sprintf("qenv: lock invariant conserved (guarded at lockguard_test.go:%d) broken at Unlock (lockguard_test.go:%d) by task %s (step %d): "+
			"the critical section left it false; fix the code between Lock and Unlock", r.guardLine, r.unlockLine, e.Task, e.Step)
		if e.Error() != want {
			t.Fatalf("seed %d:\n got %s\nwant %s", seed, e.Error(), want)
		}
		if step, ok := r.stepAtUnlock[e.Task]; !ok || step != e.Step || e.Step == 0 || e.Native {
			t.Fatalf("seed %d: step %d in task %s, which unlocked at step %d", seed, e.Step, e.Task, step)
		}
		again := runBank(t, qenv.NewSeeded(seed), "lost", true)
		if again.err == nil || *again.err != *e {
			t.Fatalf("seed %d replayed: %v, first %v", seed, again.err, e)
		}
	}
	if caught == 0 {
		t.Fatal("no seed of 20 caught the lost credit (a transfer of 3) at its Unlock")
	}
}

// A passing guarded run makes exactly the choices the unguarded one makes:
// evaluating the invariant draws nothing and schedules nothing.
func TestLockInvariantKeepsChoices(t *testing.T) {
	for _, mutant := range []string{"", "restored"} {
		for seed := range uint64(10) {
			guarded := &qenv.ChoiceLog{C: qenv.NewSeeded(seed)}
			unguarded := &qenv.ChoiceLog{C: qenv.NewSeeded(seed)}
			if r := runBank(t, guarded, mutant, true); r.err != nil || r.runErr != nil {
				t.Fatal(r.err, r.runErr)
			}
			runBank(t, unguarded, mutant, false)
			if len(guarded.Choices) == 0 || !reflect.DeepEqual(guarded.Choices, unguarded.Choices) {
				t.Fatalf("%q seed %d: guarded %v\nunguarded %v", mutant, seed, guarded.Choices, unguarded.Choices)
			}
		}
	}
}

// Every seam called from an invariant panics with the fix, naming the seam,
// the invariant and the task: a seam there would change the run's
// choices.
func TestLockInvariantRefusesSeams(t *testing.T) {
	var other sync.Mutex
	var sig qenv.Signal
	var s *qenv.Sim
	for _, c := range []struct {
		seam string
		call func()
	}{
		{"Yield", func() { qenv.Yield(context.Background()) }},
		{"Sleep", func() { qenv.Sleep(context.Background(), time.Millisecond) }},
		{"Wait", func() { qenv.Wait(context.Background(), func() bool { return true }) }},
		{"Signal.Wait", func() { sig.Wait(context.Background(), time.Millisecond) }},
		{"Go (child)", func() { qenv.Go(context.Background(), "child", func(context.Context) {}) }},
		{"a choice (pick)", func() { s.Choose("pick", 2) }},
		{"Lock", func() { woven.MutexLock(&other) }},
		{"Notify", func() { sig.Notify() }},
		{"recording a step", func() { qobs.Record(context.Background(), "inside", nil) }}, //qenv:ok the refusal of a step recorded inside an invariant, through the goroutine
	} {
		seam, call := c.seam, c.call
		t.Run(seam, func(t *testing.T) {
			s = qenv.New(qenv.Nominal{})
			defer s.Close()
			var mu sync.Mutex
			s.Spawn("waiter", func(ctx context.Context) { sig.Wait(ctx, time.Hour) }) // a Notify would wake it
			s.Spawn("holder", func(ctx context.Context) {
				qenv.Guard(ctx, &mu, "inv", func() bool { call(); return true })
				woven.MutexLock(&mu)
				woven.MutexUnlock(&mu)
			})
			var got any
			var runErr error
			func() {
				defer func() { got = recover() }()
				runErr = s.Run(time.Minute)
			}()
			msg, _ := got.(string)
			if !strings.HasPrefix(msg, "qenv: "+seam+" inside lock invariant inv's holds (guarded at lockguard_test.go:") ||
				!strings.Contains(msg, ", task holder): ") || !strings.HasSuffix(msg, "Read the state without it") {
				t.Fatalf("panic %v (Run returned %v)", got, runErr)
			}
		})
	}
}

// Guards are per simulation: two simulations running in parallel unlock
// one mutex, only one guards it with an invariant that never holds, and
// only that one fails; a guard does not outlive its simulation's Close.
func TestLockInvariantPerRun(t *testing.T) {
	var shared sync.Mutex
	run := func(t *testing.T, guard bool) error {
		s := qenv.New(qenv.NewSeeded(1))
		defer s.Close()
		if guard {
			qenv.Guard(context.Background(), &shared, "never", func() bool { return false }) // the creator's goroutine finds its simulation
		}
		for i := range 4 {
			s.Spawn(fmt.Sprint("t", i), func(ctx context.Context) {
				for range 20 {
					woven.MutexLock(&shared)
					qenv.Yield(ctx)
					woven.MutexUnlock(&shared)
				}
			})
		}
		var e *qenv.LockInvariantError
		func() {
			defer func() {
				if p := recover(); p != nil {
					e = p.(*qenv.LockInvariantError)
				}
			}()
			if err := s.Run(time.Second); err != nil {
				t.Error(err)
			}
		}()
		if e == nil {
			return nil
		}
		return e
	}
	t.Run("together", func(t *testing.T) {
		for i := range 8 {
			guard := i%2 == 0
			t.Run(fmt.Sprint("guard=", guard, "/", i), func(t *testing.T) {
				t.Parallel()
				err := run(t, guard)
				if e, ok := errors.AsType[*qenv.LockInvariantError](err); guard != ok || ok && e.Name != "never" {
					t.Errorf("guarded %v: %v", guard, err)
				}
			})
		}
	})
	if err := run(t, false); err != nil {
		t.Fatalf("after the guarded simulations closed: %v", err)
	}
}

// RLock and RUnlock are not checked (a reader publishes nothing); an
// RWMutex's Unlock is, and so is the Unlock after a TryLock.
func TestLockInvariantRWMutex(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	var rw sync.RWMutex
	var m sync.Mutex
	rwChecks, mChecks := 0, 0
	qenv.Guard(s.Context(), &rw, "rw", func() bool { rwChecks++; return true })
	qenv.Guard(s.Context(), &m, "m", func() bool { mChecks++; return true })
	s.Spawn("user", func(ctx context.Context) {
		woven.RLock(&rw)
		woven.RUnlock(&rw)
		if woven.TryRLock(&rw) {
			woven.RUnlock(&rw)
		}
		woven.RWLock(&rw)
		woven.RWUnlock(&rw)
		if woven.RWTryLock(&rw) {
			woven.RWUnlock(&rw)
		}
		if woven.MutexTryLock(&m) {
			woven.MutexUnlock(&m)
		}
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if rwChecks != 2 || mChecks != 1 {
		t.Fatalf("rw checked %d times, m %d; want 2 (its Unlocks, not its RUnlocks) and 1", rwChecks, mChecks)
	}
}

// A task killed inside its critical section (left parked at Close) unwinds
// through its deferred Unlock unchecked: its section was cut short, not
// finished.
func TestLockInvariantNotAtUnwind(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	var mu sync.Mutex
	broken, checks := false, 0
	qenv.Guard(s.Context(), &mu, "whole", func() bool { checks++; return !broken })
	s.Spawn("cut", func(ctx context.Context) {
		woven.MutexLock(&mu)
		defer woven.MutexUnlock(&mu)
		broken = true
		qenv.Sleep(ctx, time.Hour)
		broken = false
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if checks != 0 {
		t.Fatalf("checked %d times while unwinding", checks)
	}
}

// On the native runtime (without a Platform) the woven unlock is checked
// as on New's scheduler; the error says it counts no steps.
func TestLockInvariantNative(t *testing.T) {
	for _, broken := range []bool{false, true} {
		s := qenv.NewNative(qenv.NewSeeded(1), nil)
		k := &bank{a: bankTotal}
		qenv.Guard(context.Background(), &k.mu, "conserved", func() bool { return k.a+k.b == bankTotal })
		for i := range 2 {
			s.Spawn(fmt.Sprint("transfer-", i), func(ctx context.Context) {
				woven.MutexLock(&k.mu)
				k.a -= 5
				qenv.Sleep(ctx, time.Millisecond)
				if !broken {
					k.b += 5
				}
				woven.MutexUnlock(&k.mu)
			})
		}
		var got any
		var runErr error
		func() {
			defer func() { got = recover() }()
			runErr = s.Run(50 * time.Millisecond)
		}()
		s.Close()
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
		e, _ := got.(*qenv.LockInvariantError)
		switch {
		case !broken && got != nil:
			t.Fatalf("faithful: %v", got)
		case broken && (e == nil || !e.Native || !strings.Contains(e.Error(), "(native runtime): the critical section left it false")):
			t.Fatalf("broken: %v", got)
		}
	}
}

// Outside a simulation Guard does nothing; under a Platform (gosim, which
// simulates sync itself) it refuses rather than check nothing; and it
// takes only the locks qenv simulates.
func TestGuardRefusals(t *testing.T) {
	var mu sync.Mutex
	qenv.Guard(context.Background(), &mu, "nothing", func() bool { return false })
	woven.MutexLock(&mu) // outside a simulation, the real lock
	woven.MutexUnlock(&mu)

	refused := func(f func()) (msg string) {
		defer func() { msg = fmt.Sprint(recover()) }()
		f()
		return ""
	}
	s := qenv.NewNative(qenv.Nominal{}, noPlatform{})
	msg := refused(func() { qenv.Guard(s.Context(), &mu, "gosim", func() bool { return true }) })
	s.Close()
	if !strings.HasPrefix(msg, `qenv: Guard("gosim") on a native simulation with a Platform (gosim): gosim simulates sync itself`) {
		t.Errorf("under a Platform: %s", msg)
	}
	s = qenv.New(qenv.Nominal{})
	var rw sync.RWMutex
	if msg := refused(func() { qenv.Guard(s.Context(), rw.RLocker(), "reader", func() bool { return true }) }); !strings.HasPrefix(msg, "qenv: Guard of a *sync.rlocker: only a *sync.Mutex or a *sync.RWMutex") {
		t.Errorf("a Locker over the lock: %s", msg)
	}
	ctx := s.Context()
	s.Close()
	if msg := refused(func() { qenv.Guard(ctx, &mu, "late", func() bool { return true }) }); !strings.HasPrefix(msg, `qenv: Guard("late") after its simulation closed`) {
		t.Errorf("after Close: %s", msg)
	}
}

// noPlatform stands in for gosim's: Guard refuses before any machine starts.
type noPlatform struct{}

func (noPlatform) Machine(string, func()) qenv.Machine { panic("no machines") }
