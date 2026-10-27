package library

import (
	"context"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
)

// doubles are the runs' doubles of the App's dependencies: the mutant
// noDedupe, a mailer without its dedupe, sending every try of a key.
func doubles() gristsim.Doubles {
	return gristsim.Doubles{Mailer: grist.Handlers{Send.Handler(func(context.Context, Receipt) (Sent, error) {
		return Sent{}, nil
	})}}
}
