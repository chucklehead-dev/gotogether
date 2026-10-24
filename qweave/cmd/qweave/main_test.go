package main

import (
	"slices"
	"testing"
)

// The go command gets every argument it was given, the overlay after the
// command: the first argument after it was once overwritten by the
// overlay, testing the current directory instead of the package named.
func TestGoCommand(t *testing.T) {
	args := []string{"go", "test", "./sim/", "-run", "X"}
	if got, want := goCommand(args, "o.json"), []string{"test", "-overlay=o.json", "./sim/", "-run", "X"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if !slices.Equal(args, []string{"go", "test", "./sim/", "-run", "X"}) {
		t.Errorf("args changed: %q", args)
	}
	if got, want := goCommand([]string{"go", "test", "-c", "-o", "t"}, ""), []string{"test", "-c", "-o", "t"}; !slices.Equal(got, want) {
		t.Errorf("no overlay: got %q, want %q", got, want)
	}
}
