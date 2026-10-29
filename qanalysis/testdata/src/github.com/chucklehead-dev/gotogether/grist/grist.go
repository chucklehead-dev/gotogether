// Package grist is a stub of grist for quintraw's fixtures: Effect alone.
package grist

import "context"

// A Result is a processor's result.
type Result struct{}

// A Guarantee is an effect's.
type Guarantee int

// AtLeastOnce is a Guarantee.
const AtLeastOnce Guarantee = 1

// Effect is an effect.
func Effect(f func(ctx context.Context) error, g Guarantee) Result { return Result{} }
