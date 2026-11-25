package qenv_test

import (
	"context"
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

// fieldSite stands for where a //quint:guards field is declared: qweave
// writes the field's file:line into the woven Unlock.
const fieldSite = "bank.go:7"

// unlockField is what qweave makes of k.mu.Unlock() when the field reads
// `mu sync.Mutex //quint:guards conserved`: the field's invariants, bound
// to k, passed to its Unlock.
func unlockField(k *bank, checks *int) {
	woven.MutexUnlockGuarded(woven.GuardedMutex{Mutex: &k.mu, Invariants: []woven.Invariant{
		{Name: "bank.conserved", Site: fieldSite, Holds: func() bool { *checks++; return k.a+k.b == bankTotal }},
	}})
}

// runBankField is runBank's run with the invariant declared on the field
// (field) or not at all: the transfers unlock through unlockField or
// woven.MutexUnlock.
func runBankField(t *testing.T, c qenv.Chooser, mutant string, field bool) (r bankRun) {
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
				if !field {
					woven.MutexUnlock(&k.mu)
					continue
				}
				_, _, line, _ := runtime.Caller(0)
				r.unlockLine = line + 2
				unlockField(k, &r.checks)
			}
		})
	}
	r.runErr = s.Run(time.Second)
	return r
}

// A field's invariant holds at every Unlock of the faithful bank and of
// the mutant that restores it before the release; the mutant that leaves
// it false fails at that Unlock, named as the field declares it and where,
// replaying from its seed; without the directive the same runs pass.
func TestFieldInvariant(t *testing.T) {
	for _, mutant := range []string{"", "restored"} {
		for seed := range uint64(10) {
			r := runBankField(t, qenv.NewSeeded(seed), mutant, true)
			if r.err != nil || r.runErr != nil || r.checks != 6 || r.unlocks != 6 {
				t.Fatalf("%q seed %d: %v %v, %d checks of %d unlocks", mutant, seed, r.err, r.runErr, r.checks, r.unlocks)
			}
		}
	}
	caught := 0
	for seed := range uint64(20) {
		r := runBankField(t, qenv.NewSeeded(seed), "lost", true)
		if plain := runBankField(t, qenv.NewSeeded(seed), "lost", false); plain.err != nil || plain.runErr != nil {
			t.Fatalf("seed %d without the directive: %v %v", seed, plain.err, plain.runErr)
		}
		if r.runErr != nil {
			t.Fatal(r.runErr)
		}
		if r.err == nil {
			continue
		}
		caught++
		e := r.err
		// the Unlock's site is the call of the woven unlock (unlockField
		// here; the field's Unlock line in woven code)
		want := fmt.Sprintf("qenv: lock invariant bank.conserved (guarded at %s) broken at Unlock (lockfield_test.go:%d) by task %s (step %d): "+
			"the critical section left it false; fix the code between Lock and Unlock", fieldSite, unlockFieldLine, e.Task, e.Step)
		if e.Error() != want {
			t.Fatalf("seed %d:\n got %s\nwant %s", seed, e.Error(), want)
		}
		if step := r.stepAtUnlock[e.Task]; step != e.Step || e.Step == 0 {
			t.Fatalf("seed %d: step %d, the task unlocked at %d", seed, e.Step, step)
		}
		if again := runBankField(t, qenv.NewSeeded(seed), "lost", true); again.err == nil || *again.err != *e {
			t.Fatalf("seed %d replayed: %v, first %v", seed, again.err, e)
		}
	}
	if caught == 0 {
		t.Fatal("no seed of 20 caught the lost credit at its Unlock")
	}
}

// unlockFieldLine is the line of unlockField's woven unlock, the frame
// unlockSite names.
var unlockFieldLine = func() int {
	pc := reflect.ValueOf(unlockField).Pointer()
	_, line := runtime.FuncForPC(pc).FileLine(pc)
	return line + 1
}()

// A field's invariant draws nothing and schedules nothing: a passing run
// makes exactly the choices of the run without the directive.
func TestFieldInvariantKeepsChoices(t *testing.T) {
	for _, mutant := range []string{"", "restored"} {
		for seed := range uint64(10) {
			field := &qenv.ChoiceLog{C: qenv.NewSeeded(seed)}
			plain := &qenv.ChoiceLog{C: qenv.NewSeeded(seed)}
			if r := runBankField(t, field, mutant, true); r.err != nil || r.runErr != nil || r.checks == 0 {
				t.Fatal(r.err, r.runErr, r.checks)
			}
			runBankField(t, plain, mutant, false)
			if len(field.Choices) == 0 || !reflect.DeepEqual(field.Choices, plain.Choices) {
				t.Fatalf("%q seed %d: with the directive %v\nwithout %v", mutant, seed, field.Choices, plain.Choices)
			}
		}
	}
}

// A seam inside a field's invariant is refused as inside a Guard's; a
// Guard of the same mutex is checked first; outside a simulation the
// woven unlock is the real Unlock and runs no invariant; a task unwinding
// after a crash is not checked.
func TestFieldInvariantSeamsAndOrder(t *testing.T) {
	for _, c := range []struct {
		seam string
		call func()
	}{
		{"Yield", func() { qenv.Yield(context.Background()) }},
		{"Sleep", func() { qenv.Sleep(context.Background(), time.Millisecond) }},
		{"recording a step", func() { qobs.Record(context.Background(), "inside", nil) }}, //qenv:ok the refusal of a step recorded inside an invariant, through the goroutine
	} {
		s := qenv.New(qenv.Nominal{})
		var mu sync.Mutex
		s.Spawn("holder", func(ctx context.Context) {
			woven.MutexLock(&mu)
			woven.MutexUnlockGuarded(woven.GuardedMutex{Mutex: &mu, Invariants: []woven.Invariant{{Name: "T.inv", Site: fieldSite, Holds: func() bool { c.call(); return true }}}})
		})
		var got any
		var runErr error
		func() {
			defer func() { got = recover() }()
			runErr = s.Run(time.Minute)
		}()
		s.Close()
		msg, _ := got.(string)
		if !strings.HasPrefix(msg, "qenv: "+c.seam+" inside lock invariant T.inv's holds (guarded at "+fieldSite+", task holder): ") {
			t.Errorf("%s: %v (Run returned %v)", c.seam, got, runErr)
		}
	}

	s := qenv.New(qenv.Nominal{})
	var rw sync.RWMutex
	var order []string
	qenv.Guard(s.Context(), &rw, "registered", func() bool { order = append(order, "registered"); return true })
	s.Spawn("user", func(ctx context.Context) {
		woven.RWLock(&rw)
		woven.RWUnlockGuarded(woven.GuardedRWMutex{RWMutex: &rw, Invariants: []woven.Invariant{
			{Name: "T.a", Site: fieldSite, Holds: func() bool { order = append(order, "a"); return true }},
			{Name: "T.b", Site: fieldSite, Holds: func() bool { order = append(order, "b"); return false }},
		}})
	})
	var e *qenv.LockInvariantError
	var runErr error
	func() {
		defer func() { e, _ = recover().(*qenv.LockInvariantError) }()
		runErr = s.Run(time.Minute)
	}()
	s.Close()
	if e == nil || e.Name != "T.b" || e.Guarded != fieldSite || strings.Join(order, " ") != "registered a b" {
		t.Fatalf("RWMutex field: %v (Run returned %v), checked %v", e, runErr, order)
	}

	var mu sync.Mutex
	ran := false
	woven.MutexLock(&mu) // outside a simulation: the real lock
	woven.MutexUnlockGuarded(woven.GuardedMutex{Mutex: &mu, Invariants: []woven.Invariant{{Name: "T.never", Site: fieldSite, Holds: func() bool { ran = true; return false }}}})
	if ran || !mu.TryLock() {
		t.Fatalf("outside a simulation: invariant ran %v, or the real mutex stayed locked", ran)
	}

	mu.Unlock()
	s = qenv.New(qenv.Nominal{})
	checks, broken := 0, false
	s.Spawn("cut", func(ctx context.Context) {
		woven.MutexLock(&mu)
		defer woven.MutexUnlockGuarded(woven.GuardedMutex{Mutex: &mu, Invariants: []woven.Invariant{{Name: "T.whole", Site: fieldSite, Holds: func() bool { checks++; return !broken }}}})
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
