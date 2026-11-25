package guarded_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"example.com/syncfixture/guarded"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// lineOf is the line of fields.go holding marker: where the directive is,
// and where the Unlock it is checked at.
func lineOf(t *testing.T, marker string) int {
	t.Helper()
	b, err := os.ReadFile("fields.go")
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, marker) {
			return i + 1
		}
	}
	t.Fatalf("fields.go has no %q", marker)
	return 0
}

// purses runs two depositors, two deposits each, the amounts chosen, into
// a Purse (the directive) or a PlainPurse, and returns the lock invariant
// error the run panicked with.
func purses(t *testing.T, c qenv.Chooser, mutant string, plain bool) (e *qenv.LockInvariantError) {
	t.Helper()
	s := qenv.New(c)
	defer s.Close()
	defer func() {
		if p := recover(); p != nil {
			e = p.(*qenv.LockInvariantError)
		}
	}()
	depositors(s, mutant, plain)
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	return nil
}

func depositors(s *qenv.Sim, mutant string, plain bool) {
	p, pp := &guarded.Purse{Mutant: mutant}, &guarded.PlainPurse{Mutant: mutant}
	for i := range 2 {
		s.Spawn(fmt.Sprint("depositor-", i), func(ctx context.Context) {
			for range 2 {
				n := s.Choose("amount", 5) + 1
				if plain {
					pp.Deposit(ctx, n)
				} else {
					p.Deposit(ctx, n)
				}
			}
		})
	}
}

// The field's invariant needs no registration: the faithful Purse and the
// mutant that restores it before the Unlock pass; the mutant that leaves
// it false fails at the Unlock, named and placed as the field declares it,
// the same way from the same seed; PlainPurse, the same code without the
// directive, passes.
func TestFieldInvariant(t *testing.T) {
	field, unlock := lineOf(t, "//quint:guards ledgered"), lineOf(t, "// the release the invariant is checked at")
	for seed := range uint64(10) {
		for _, m := range []string{"", "restored"} {
			if e := purses(t, qenv.NewSeeded(seed), m, false); e != nil {
				t.Fatalf("%q seed %d: %v", m, seed, e)
			}
		}
	}
	caught := 0
	for seed := range uint64(20) {
		if e := purses(t, qenv.NewSeeded(seed), "unledgered", true); e != nil {
			t.Fatalf("seed %d, no directive: %v", seed, e)
		}
		e := purses(t, qenv.NewSeeded(seed), "unledgered", false)
		if e == nil {
			continue
		}
		caught++
		want := fmt.Sprintf("qenv: lock invariant Purse.ledgered (guarded at fields.go:%d) broken at Unlock (fields.go:%d) by task %s (step %d): "+
			"the critical section left it false; fix the code between Lock and Unlock", field, unlock, e.Task, e.Step)
		if e.Error() != want || !strings.HasPrefix(e.Task, "depositor-") {
			t.Fatalf("seed %d:\n got %s\nwant %s", seed, e.Error(), want)
		}
		if again := purses(t, qenv.NewSeeded(seed), "unledgered", false); again == nil || *again != *e {
			t.Fatalf("seed %d replayed: %v, first %v", seed, again, e)
		}
	}
	if caught == 0 {
		t.Fatal("no seed of 20 caught the unledgered deposit (of 3) at its Unlock")
	}
}

// A passing run with the directive makes exactly the choices of the same
// run without it.
func TestFieldInvariantKeepsChoices(t *testing.T) {
	for seed := range uint64(10) {
		for _, m := range []string{"", "restored"} {
			field, plain := &qenv.ChoiceLog{C: qenv.NewSeeded(seed)}, &qenv.ChoiceLog{C: qenv.NewSeeded(seed)}
			if e := purses(t, field, m, false); e != nil {
				t.Fatal(e)
			}
			purses(t, plain, m, true)
			if len(field.Choices) == 0 || !reflect.DeepEqual(field.Choices, plain.Choices) {
				t.Fatalf("%q seed %d: with the directive %v\nwithout %v", m, seed, field.Choices, plain.Choices)
			}
		}
	}
}

// A seam inside the field's invariant is refused, naming it.
func TestFieldInvariantRefusesSeams(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	p := &guarded.Purse{Probe: func() { qenv.Yield(context.Background()) }}
	s.Spawn("depositor", func(ctx context.Context) { p.Deposit(ctx, 1) })
	var got any
	func() {
		defer func() { got = recover() }()
		_ = s.Run(time.Second)
	}()
	msg, _ := got.(string)
	if want := fmt.Sprintf("qenv: Yield inside lock invariant Purse.ledgered's holds (guarded at fields.go:%d, task depositor): ", lineOf(t, "//quint:guards ledgered")); !strings.HasPrefix(msg, want) {
		t.Fatalf("got %v\nwant %s...", got, want)
	}
}

// A local value's deferred Unlock, and a read-write lock's Unlock handed
// back as a method value (the directive on the field's doc), are checked;
// its RUnlock is not; outside a simulation the woven Unlocks are the real
// ones and check nothing.
func TestFieldInvariantForms(t *testing.T) {
	run := func(f func(ctx context.Context)) (e *qenv.LockInvariantError) {
		s := qenv.New(qenv.Nominal{})
		defer s.Close()
		defer func() {
			if p := recover(); p != nil {
				e = p.(*qenv.LockInvariantError)
			}
		}()
		s.Spawn("user", f)
		if err := s.Run(time.Minute); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	for _, forget := range []bool{false, true} {
		e := run(func(ctx context.Context) { guarded.DepositOnce(ctx, 2, forget) })
		if forget != (e != nil) || e != nil && e.Name != "Purse.ledgered" {
			t.Errorf("DepositOnce forget=%v: %v", forget, e)
		}
		tl := &guarded.Tally{}
		e = run(func(ctx context.Context) {
			tl.Add(ctx, false)
			tl.Add(ctx, forget)
			tl.Count()
		})
		if forget != (e != nil) || e != nil && (e.Name != "Tally.counted" || e.Guarded != fmt.Sprintf("fields.go:%d", lineOf(t, "mu   sync.RWMutex"))) {
			t.Errorf("Tally forget=%v: %v", forget, e)
		}
	}
	if got := guarded.DepositOnce(context.Background(), 2, true); got != 2 {
		t.Errorf("outside a simulation: %d", got)
	}
	tl := &guarded.Tally{}
	tl.Add(context.Background(), true) // real locks, real time; nothing checked
	if tl.Count() != 1 {
		t.Errorf("outside a simulation: %d", tl.Count())
	}
}

// purseSystem is depositors as a qtest.System, every run checked by the
// account model (which sees the balance only: the entries are the lock
// invariant's alone).
func purseSystem(mutant string) qtest.System {
	return qtest.System{Binding: "../../../../validate/testdata/account.yaml", Mutant: mutant, Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		depositors(s, mutant, false)
		return s.Run(time.Second)
	}}
}

// capture is a testing.TB that keeps an exploration's failures.
type capture struct {
	testing.TB
	errs   []string
	failed bool
}

func (c *capture) Helper() {}
func (c *capture) Errorf(format string, a ...any) {
	c.errs = append(c.errs, fmt.Sprintf(format, a...))
	c.failed = true
}
func (c *capture) Fatalf(format string, a ...any) { c.Errorf(format, a...); runtime.Goexit() }
func (c *capture) Fatal(a ...any)                 { c.Errorf("%s", fmt.Sprint(a...)); runtime.Goexit() }
func (c *capture) FailNow()                       { c.failed = true; runtime.Goexit() }
func (c *capture) Failed() bool                   { return c.failed }
func (c *capture) Logf(string, ...any)            {}

// Through an exploration: the faithful and restoring purses conform, the
// unledgered one fails a run at its Unlock, with the seed that reproduces
// it, though the model alone cannot see the entries.
func TestFieldInvariantExplore(t *testing.T) {
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("needs quint")
	}
	cfg := qtest.ExploreConfig{Seeds: 8, Parallel: 4}
	purseSystem("").Explore(t, cfg).Expect(t, qtest.Conforms())
	purseSystem("restored").Explore(t, cfg).Expect(t, qtest.Conforms())

	c := &capture{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok capture's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		purseSystem("unledgered").Explore(c, cfg)
	}()
	<-done
	all := strings.Join(c.errs, "\n")
	for _, want := range []string{
		fmt.Sprintf("panicked: qenv: lock invariant Purse.ledgered (guarded at fields.go:%d) broken at Unlock (fields.go:%d) by task depositor-",
			lineOf(t, "//quint:guards ledgered"), lineOf(t, "// the release the invariant is checked at")),
		"reproduce: GOTOGETHER_RUN='TestFieldInvariantExplore#3:seed=",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("lacks %q:\n%s", want, all)
		}
	}
}
