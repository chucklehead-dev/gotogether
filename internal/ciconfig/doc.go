// Package ciconfig holds the checks of the repository's CI configuration
// that a push can run: a workflow file GitHub cannot load fails no job of
// its own, it just never runs (the nightly once had no jobs at all for a
// step name with an unquoted colon).
package ciconfig
