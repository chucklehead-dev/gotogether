package retrystorm

import (
	"context"
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/retrystorm/retry"
	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"hegel.dev/go/hegel"
)

func budget(m retry.Mutation) *retry.Budget {
	return &retry.Budget{Ratio: BudgetRatio, Max: BudgetMax, Mutation: m}
}

// The budget's steps are transitions of model/retry.qnt, with its balance,
// and keep the amplification bound and the cap.
func TestBudgetConforms(t *testing.T) {
	qtest.RequireQuint(t)
	qtest.Record(t, "retrystorm.binding.yaml", func(*qobs.Recorder) { Burst(budget(retry.Faithful)) }).Expect(t, qtest.Conforms())
}

// Uncapped: the 100th deposit of the quiet period overfills the bucket.
func TestBudgetCatchesUncapped(t *testing.T) {
	qtest.RequireQuint(t)
	run := qtest.Record(t, "retrystorm.binding.yaml", func(*qobs.Recorder) { Burst(budget(retry.Uncapped)) })
	run.Expect(t, qtest.Mismatch("request"))
}

// DepositOnRetry: the first admitted retry leaves a balance the model does
// not have.
func TestBudgetCatchesDepositOnRetry(t *testing.T) {
	qtest.RequireQuint(t)
	run := qtest.Record(t, "retrystorm.binding.yaml", func(*qobs.Recorder) { Burst(budget(retry.DepositOnRetry)) })
	run.Expect(t, qtest.Mismatch("retry"))
}

// machine drives a budget with drawn bursts of requests and retries.
type machine struct{ b *retry.Budget }

func (m *machine) RuleRequests(tc hegel.TestCase) {
	for n := hegel.Draw(tc, hegel.Integers(1, 120)); n > 0; n-- {
		m.b.Deposit(context.Background())
	}
}

func (m *machine) RuleRetries(tc hegel.TestCase) {
	for n := hegel.Draw(tc, hegel.Integers(1, 15)); n > 0; n-- {
		m.b.Withdraw(context.Background())
	}
}

const budgetBinding = "retrystorm.binding.yaml"

var hegelOpts = []qtest.Option{qtest.Global()}

// Hegel drives the faithful budget; every run satisfies the model.
func TestHegelBudgetConforms(t *testing.T) {
	qtest.RequireQuint(t)
	qhegel.Test(t, qhegel.Config{Binding: budgetBinding, Mode: qhegel.After, Opts: hegelOpts, AllowEmpty: true}, func(ht *hegel.T, _ *qhegel.Case) {
		hegel.RunStateful(ht, &machine{b: budget(retry.Faithful)}, hegel.WithStatefulStepCount(6))
	}, hegel.WithTestCases(20), hegel.WithDatabase(""))
}

// Hegel finds the Uncapped mutant and shrinks it to a run that overfills
// the bucket.
func TestHegelCatchesUncapped(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("shrinking against the model runs quint ~100 times")
	}
	v := qhegel.FindViolation(t, qhegel.ViolationConfig{Binding: budgetBinding, Opts: hegelOpts, AllowEmpty: true}, func(tc hegel.TestCase, _ *qhegel.Case) {
		hegel.RunStateful(tc, &machine{b: budget(retry.Uncapped)}, hegel.WithStatefulStepCount(6))
	}, hegel.WithTestCases(50))
	if last := v.Last(); last.Action != "request" {
		t.Errorf("shrunk run ends with %s, want the request that overfills the bucket:\n%s", last.Action, v.Summary)
	}
}
