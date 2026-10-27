package library

import "github.com/chucklehead-dev/gotogether/grist/gristsim"

// doubles are the runs' doubles of the App's dependencies: none, so
// gristsim serves the mailer its default double (grist.DefaultHandlers:
// each key sent once, a later try of it replayed). The noDedupe mutant
// replaces this file with a mailer that sends every try.
func doubles() gristsim.Doubles { return nil }
