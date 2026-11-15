package bank

import (
	"context"
	"fmt"
	"testing"

	"github.com/chucklehead-dev/gotogether/connect"
	"github.com/chucklehead-dev/gotogether/qtest"
)

var accounts = []string{"alice", "bob", "carol"}

// driver replays model steps on a Bank: quint-connect's Driver.
type driver struct {
	bank     *Bank
	mutation Mutation
}

func (d *driver) Step(s *connect.Step) error {
	ctx := context.Background()
	return connect.Switch{
		"init": func(*connect.Step) error {
			d.bank = New(accounts, WithMutation(d.mutation))
			return nil
		},
		"deposit": func(s *connect.Step) error {
			return d.bank.Deposit(ctx, connect.MustPick[string](s, "account"), connect.MustPick[int](s, "amount"))
		},
		"withdraw": func(s *connect.Step) error {
			return d.bank.Withdraw(ctx, connect.MustPick[string](s, "account"), connect.MustPick[int](s, "amount"))
		},
		"transfer": func(s *connect.Step) error {
			return d.bank.Transfer(ctx, connect.MustPick[string](s, "src"), connect.MustPick[string](s, "dst"), connect.MustPick[int](s, "amount"))
		},
	}.Step(s)
}

// modelState is the part of the model's state the bank has. It is decoded
// from the model's state after every step and compared with State().
type modelState struct {
	Balances map[string]int
}

func (d *driver) State() (any, error) { return modelState{Balances: d.bank.Balances()}, nil }

// readingDriver is the driver that also checks the bank's other read.
// Its CheckStep needs the model connect.Run compiles for Step.Eval, so
// the drivers Replay and Hegel run (which compile none) are plain ones.
type readingDriver struct{ *driver }

// CheckStep reads each account through Balance, the bank's own read, and
// compares it with the model's definition balanceOf, evaluated in the
// step's state (Step.Eval): State covers Balances, this the other read.
func (d readingDriver) CheckStep(s *connect.Step) error {
	for _, a := range accounts {
		if model, bank := connect.MustEval[int](s, "balanceOf", a), d.bank.Balance(a); model != bank {
			return fmt.Errorf("Balance(%q) = %d, the model's balanceOf = %d", a, bank, model)
		}
	}
	return nil
}

// The model drives the bank: random traces of the deployed design, each
// replayed on a fresh bank with the balances compared after every step.
// The run must reach the model's rich state (Require): the shortest path
// to it is replayed first (subtest witness_rich), and the log says how
// many of the random traces reached it. Reproduce a failure with the
// QUINT_SEED it prints.
func TestModelDrivesBank(t *testing.T) {
	qtest.RequireQuint(t)
	connect.Run(t, connect.Config{Spec: "model/bank.qnt", Main: "bankDesign", MaxSamples: 30, MaxSteps: 25, Require: []string{"rich"}},
		func(testing.TB) connect.Driver { return readingDriver{&driver{}} })
}

// What model-driven testing can and cannot catch. LostDeposit diverges
// from the model on a deposit, which the model does take: caught, with
// the step and the field. SelfTransfer is the code accepting something the
// model forbids (src == dst). The model never asks for a forbidden step,
// so no model trace can expose it: that takes the code driving and the
// model judging (hegel_test.go's TestHegelCatchesSelfTransfer).
func TestModelDrivenCatchesOnlyDivergence(t *testing.T) {
	qtest.RequireQuint(t)
	files, c, err := connect.GenerateRun(t.Context(), connect.Config{Spec: "model/bank.qnt", Main: "bankDesign",
		MaxSamples: 40, MaxSteps: 25, Seed: "0x5eed", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	caught := func(m Mutation) bool {
		for i, f := range files {
			tr, err := readTrace(f)
			if err != nil {
				t.Fatal(err)
			}
			if err := connect.Replay(tr, &driver{mutation: m}, connect.ReplayOptions{Trace: i}); err != nil {
				t.Logf("%s caught (seed %s): %v", m, c.Seed, err)
				return true
			}
		}
		return false
	}
	if !caught(LostDeposit) {
		t.Errorf("no model trace exposed the %s mutant", LostDeposit)
	}
	if caught(SelfTransfer) {
		t.Errorf("a model trace asked for a self-transfer, which the model forbids")
	}
}
