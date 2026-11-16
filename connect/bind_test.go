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
		State(func(b *bank) bankState { return bankState{Balances: b.balances, Ops: b.ops} })
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
	b.Run(t, Config{Spec: "testdata/bank.qnt", MaxSamples: 5, MaxSteps: 12, Seed: "0x1"})
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
		{"returns unbound", func() { Bind(func(testing.TB) *bank { return nil }).Returns("deposit", "balanceOf") }, "no action bound to a method"},
		{"returns no result", func() {
			Bind(func(testing.TB) *bank { return nil }).Action("withdraw", (*bank).withdraw, "account", "amount").Returns("withdraw", "balanceOf")
		}, "returns 0 results besides an error"},
		{"observes unbound", func() { Bind(func(testing.TB) *bank { return nil }).Observes("deposit", "account") }, "no action bound to a method"},
		{"observes count", func() {
			Bind(func(testing.TB) *bank { return nil }).Action("deposit", (*bank).depositBalance, "account", "amount").Observes("deposit", "account", "amount")
		}, "returns 1 result besides an error, but 2 picks named"},
		{"observes and returns", func() {
			Bind(func(testing.TB) *bank { return nil }).Action("deposit", (*bank).depositBalance, "account", "amount").Returns("deposit", "balanceOf", "account").Observes("deposit", "amount")
		}, "already checked"},
		{"state not a func", func() { Bind(func(testing.TB) *bank { return nil }).State(bankState{}) }, "connect.Bind: State: connect.bankState is no func(*connect.bank) S"},
		{"state of another type", func() {
			Bind(func(testing.TB) *bank { return nil }).State(func(*bankDriver) bankState { return bankState{} })
		}, "is no func(*connect.bank) S"},
		{"state twice", func() {
			bankBinding(false, (*bank).deposit).State(func(*bank) bankState { return bankState{} })
		}, "connect.Bind: State given twice: give one projection"},
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

// An action's result that is a choice the implementation makes, which
// the model picks, is compared with the pick (Observes): the faithful
// choice passes, another is named with the model's, and a pick the step
// lacks is named with the picks there are.
func TestBindObserves(t *testing.T) {
	// claim chooses the account to deposit into: the implementation's
	// choice, which the model picks as account
	claim := func(choose func(string) string) *Binding[*bank] {
		return Bind(func(testing.TB) *bank { return &bank{balances: map[string]int{"alice": 0, "bob": 0}} }).
			Action("claim", func(b *bank, preferred string, n int) string {
				a := choose(preferred)
				b.deposit(a, n)
				return a
			}, "preferred", "amount").
			Observes("claim", "account")
	}
	step := func(picks map[string]itf.Value) *Step { return &Step{Action: "claim", Picks: picks} }
	picks := map[string]itf.Value{"preferred": itf.StrOf("bob"), "amount": itf.IntOf(2), "account": itf.StrOf("bob")}
	for _, c := range []struct {
		name   string
		choose func(string) string
		picks  map[string]itf.Value
		want   string // "" for a pass
	}{
		{"faithful", func(p string) string { return p }, picks, ""},
		{"another choice", func(string) string { return "alice" }, picks, "the implementation chose account = alice; the model's choice is bob"},
		{"no pick", func(p string) string { return p }, map[string]itf.Value{"preferred": itf.StrOf("bob"), "amount": itf.IntOf(2)}, "Observes' pick account is not in the step (have amount, preferred)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := claim(c.choose).Driver(t)
			if err := d.Step(&Step{Action: "init"}); err != nil {
				t.Fatal(err)
			}
			err := d.Step(step(c.picks))
			switch {
			case c.want == "" && err != nil:
				t.Errorf("the faithful choice: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Errorf("got %v, want %q", err, c.want)
			}
		})
	}
	// an observed result is checked: the driver is made without refusal
	ft := &fatalTB{TB: t}
	claim(func(p string) string { return p }).Driver(ft)
	if ft.fatal != "" {
		t.Errorf("an observed result refused as unchecked: %s", ft.fatal)
	}
}
