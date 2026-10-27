//go:build !linux

package main

// becomeSubreaper does nothing outside Linux: orphaned descendants are
// reparented to init and not counted.
func becomeSubreaper() error { return nil }
