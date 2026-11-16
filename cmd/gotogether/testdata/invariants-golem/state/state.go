// Package state carries both models' annotations, each qualified by its
// model's module.
package state

// Restore puts the attempts back after a restart.
//
//quint:enforces attempts.noLostAttempt
func Restore(n int) int { return max(n, 0) }

// Serve serves a page whose lease holds.
//
//quint:enforces publication.noExpiredServe
func Serve(expired bool) bool { return !expired }
