package woven

import "github.com/chucklehead-dev/gotogether/internal/wovenrt"

// A Mutant is one change qweave -mutate wove in; its ID is assigned by
// RegisterMutants.
type Mutant = wovenrt.Mutant

// RegisterMutants adds a file's mutants and returns the ID of the first:
// woven code numbers them from it. With GOTOGETHER_MUTANT=list each is
// printed to standard error as it registers.
func RegisterMutants(ms []Mutant) int { return wovenrt.RegisterMutants(ms) }

// MutantOn reports whether mutant id is the calling code's run's mutant
// (qmut.WithMutant, or GOTOGETHER_MUTANT=<id> for the process), and notes
// that its code was reached (qmut.WithReach). A dropped call is
// `if !MutantOn(id) { call }`.
func MutantOn(id int) bool { return wovenrt.MutantOn(id) }

// Cond is an if's condition c, negated when mutant id is on.
func Cond(id int, c bool) bool {
	if MutantOn(id) {
		return !c
	}
	return c
}

// Pick is orig, or mutated when mutant id is on: a comparison with its
// boundary moved.
func Pick(id int, orig, mutated bool) bool {
	if MutantOn(id) {
		return mutated
	}
	return orig
}
