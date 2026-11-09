// Package ruledrop names the rule a generated mutant's test binary runs
// without its declared refusals (gristtest.RuleMutants: a mutant per rule). It is
// set by gristtest alone, which only test binaries import: no production
// binary can drop a rule.
package ruledrop

// Dropped is the rule whose declared refusals grist ignores, "" for none.
// Its callback still runs, preserving reads and operational errors. Process-wide,
// set once by gristtest's init (from GOTOGETHER_MUTANT=drop-<rule>, qtest.Mutant) before any
// rule runs, read-only after.
var Dropped string
