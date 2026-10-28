// Package ruledrop names the rule a generated mutant's test binary runs
// without (gristtest.RuleMutants: a mutant per rule, the rule dropped). It is
// set by gristtest alone, which only test binaries import: no production
// binary can drop a rule.
package ruledrop

// Dropped is the name of the rule grist skips, "" for none: process-wide,
// set once by gristtest's init (from GOTOGETHER_MUTANT=drop-<rule>, qtest.Mutant) before any
// rule runs, read-only after.
var Dropped string
