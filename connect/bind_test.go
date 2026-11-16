package connect

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/itf"
)

// depositBalance is deposit returning the balance after it, which a
// Binding compares with the model's balanceOf (Returns).
func (b *bank) depositBalance(a string, n int) int { b.deposit(a, n); return b.balances[a] }

// depositBalanceBefore returns the balance before the deposit: the result
// Returns must catch.
func (b *bank) depositBalanceBefore(a string, n int) int {
	before := b.balances[a]
	b.deposit(a, n)
	return before
}

// bindBuggySamples is how many traces bind-buggy's run samples: with
// seed 0x1, enough to reach a transfer of 3, where the buggy bank credits
// twice (TestBindCatchesDivergence says so if a change of the sampler
// moves it out of reach).
const bindBuggySamples = 50

// bankBinding is the test bank bound to bank.qnt's actions, with deposit
// as given (it may return a balance) and the state projected.
func bankBinding[F any](buggy bool, deposit F) *Binding[*bank] {
	return Bind(func(testing.TB) *bank { return &bank{balances: map[string]int{"alice": 0, "bob": 0}, buggy: buggy} }).
		Action("deposit", deposit, "account", "amount").
		Action("withdraw", (*bank).withdraw, "account", "amount").
		Action("transfer", (*bank).transfer, "src", "dst", "amount").
		State(func(b *bank) any { return bankState{Balances: b.balances, Ops: b.ops} })
}

// A Binding drives the bank as bankDriver's Switch does: each action a
// method, its picks its parameters by name, the state projected, and a
// check with the model's own def after every step (Step.Eval).
func TestBindDrivesBank(t *testing.T) {
	needQuint(t)
	// deposit returns the balance after it, compared with the model's
	// balanceOf (Returns): the faithful result passes in the same run
	b := bankBinding(false, (*bank).depositBalance).Returns("deposit", "balanceOf", "account").Check(func(b *bank, s *Step) error {
		for _, a := range []string{"alice", "bob"} {
			if got, want := b.balances[a], MustEval[int](s, "balanceOf", a); got != want {
				t.Errorf("%s: %d, the model's %d", a, got, want)
			}
		}
		return nil
	})
	Run(t, Config{Spec: "testdata/bank.qnt", MaxSamples: 5, MaxSteps: 12, Seed: "0x1"}, b.Driver)
}

// The bound bank diverges where bankDriver's does: a transfer of 3 credits
// twice, and the step, the action and its picks are named.
func TestBindCatchesDivergence(t *testing.T) {
	needQuint(t)
	out := connectFailureOutput(t, "bind-buggy")
	if !strings.Contains(out, "transfer(") || !strings.Contains(out, "amount=3") {
		t.Errorf("want the diverging transfer named:\n%s", out)
	}
}

// An action's result is compared with the model's def (Returns), so a
// method returning the balance before the deposit fails, naming both
// (the faithful one passes in TestBindDrivesBank); one whose result
// nothing checks is refused before any step, since it would read as a
// pass.
func TestBindChecksResults(t *testing.T) {
	needQuint(t)
	out := connectFailureOutput(t, "bind-returns-wrong")
	if !strings.Contains(out, "connect.Bind: action deposit returned") || !strings.Contains(out, "the model's balanceOf(") {
		t.Errorf("want the wrong result named:\n%s", out)
	}
	// refused when the driver is made, before any trace: no run needed
	ft := &fatalTB{TB: t}
	bankBinding(false, (*bank).depositBalance).Driver(ft)
	if !strings.Contains(ft.fatal, "returns int besides an error, and nothing checks it") {
		t.Errorf("want the unchecked result refused, got %q", ft.fatal)
	}
}

// fatalTB records a Fatalf instead of ending the test: Driver's refusal
// is the call under test.
type fatalTB struct {
	testing.TB
	fatal string
}

func (f *fatalTB) Fatalf(format string, args ...any) { f.fatal = fmt.Sprintf(format, args...) }

// A malformed binding panics where it is built, naming the fix.
func TestBindRefusesMalformed(t *testing.T) {
	type other struct{}
	for _, c := range []struct {
		name string
		bind func()
		want string
	}{
		{"arity", func() { Bind(func(testing.TB) *bank { return nil }).Action("deposit", (*bank).deposit, "account") }, "takes 2 parameters besides the receiver, but 1 pick is named"},
		{"receiver", func() {
			Bind(func(testing.TB) *bank { return nil }).Action("deposit", func(*other, string) {}, "account")
		}, "not the implementation's *connect.bank"},
		{"not a func", func() { Bind(func(testing.TB) *bank { return nil }).Action("deposit", 3) }, "is no func of the implementation"},
		{"twice", func() {
			Bind(func(testing.TB) *bank { return nil }).Action("deposit", (*bank).deposit, "account", "amount").Action("deposit", (*bank).deposit, "account", "amount")
		}, "bound twice"},
		{"init", func() { Bind(func(testing.TB) *bank { return nil }).Skip("init") }, "init is Bind's constructor"},
		{"returns unbound", func() { Bind(func(testing.TB) *bank { return nil }).Returns("deposit", "balanceOf") }, "no such action"},
		{"returns no result", func() {
			Bind(func(testing.TB) *bank { return nil }).Action("withdraw", (*bank).withdraw, "account", "amount").Returns("withdraw", "balanceOf")
		}, "returns 0 results besides an error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if s, _ := r.(string); !strings.Contains(s, c.want) {
					t.Errorf("panic %v, want %q", r, c.want)
				}
			}()
			c.bind()
		})
	}
}

// A pick the model did not make is an error naming the picks there are,
// unless its parameter is a pointer: optional, nil.
func TestBindPicks(t *testing.T) {
	var got []*int
	b := Bind(func(testing.TB) *bank { return &bank{balances: map[string]int{}} }).
		Action("deposit", (*bank).deposit, "account", "amount").
		Handle("note", func(*bank, *Step) error { return nil }).
		Action("maybe", func(_ *bank, n *int) { got = append(got, n) }, "n")
	d := b.Driver(t)
	if err := d.Step(&Step{Action: "init"}); err != nil {
		t.Fatal(err)
	}
	err := d.Step(&Step{Action: "deposit", Picks: map[string]itf.Value{"account": itf.StrOf("alice")}})
	if err == nil || !strings.Contains(err.Error(), "parameter 2 (pick amount)") || !strings.Contains(err.Error(), "have account") {
		t.Errorf("a missing pick: %v", err)
	}
	if err := d.Step(&Step{Action: "maybe"}); err != nil || len(got) != 1 || got[0] != nil {
		t.Errorf("an optional pick the model did not make: %v, %v", err, got)
	}
	if err := d.Step(&Step{Action: "unbound"}); err == nil || !strings.Contains(err.Error(), "unbound") {
		t.Errorf("an unbound action: %v", err)
	}
}
