// Package bindingfix has quintaction's binding findings (against
// testdata/bank.binding.yaml) with their fixes: a misspelled action
// named as the model's nearest, in a directive and in a Record call, a
// field the binding uses recorded from the parameter or named result of
// its name; and one without (a field the function has nothing of).
package bindingfix

import (
	"context"

	"github.com/chucklehead-dev/gotogether/qobs"
)

// Bank keeps balances.
type Bank struct{ balances map[string]int }

/* want `deposti is not an action of bank \(did you mean deposit\?\)` */      //quint:action action:deposti account:account amount:amount obs.balance:b.balances[account]
func (b *Bank) Deposit(ctx context.Context, account string, amount int) error { return nil }

/* want `the binding for withdraw uses .amount, which this annotation does not record` */ //quint:action action:withdraw account:account obs.balance:b.balances[account]
func (b *Bank) Withdraw(ctx context.Context, account string, amount int) error            { return nil }

/* want `the binding for deposit uses .obs.balance, which this annotation does not record` */ //quint:action action:deposit account:account amount:amount
func (b *Bank) Credit(ctx context.Context, account string, amount int) (balance int, err error) {
	return 0, nil
}

/* want `the binding for deposit uses .obs.balance, which this annotation does not record` */ //quint:action action:deposit account:account amount:amount
func (b *Bank) Pay(ctx context.Context, account string, amount int) error                     { return nil }

func record(ctx context.Context) {
	qobs.Record(ctx, "withdrw", nil) // want `withdrw is not an action of bank \(did you mean withdraw\?\)`
	qobs.Record(ctx, "close", nil)   // want `close is not an action of bank; its actions`
}
