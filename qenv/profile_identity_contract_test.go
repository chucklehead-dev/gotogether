package qenv_test

import (
	"fmt"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// Recursion supplies a stack with one additional return address. Capture it
// with the same caller convention as Profile.Waited, without assuming PC sizes.
//
//go:noinline
func profileIdentityWait(p *qenv.TimeProfile, depth int, name func([]uintptr) string) {
	if depth != 0 {
		profileIdentityWait(p, depth-1, name)
		return
	}
	pcs := make([]uintptr, 64)
	n := runtime.Callers(2, pcs)
	end := p.Waited(name(pcs[:n]), qenv.Epoch, func() time.Time { return qenv.Epoch.Add(time.Second) })
	end()
}

func TestProfileTaskNameCannotImpersonateStackFrame(t *testing.T) {
	p := qenv.NewTimeProfile()
	var recursivePC uintptr
	var recursive, flat []uintptr
	for _, depth := range []int{1, 0} {
		profileIdentityWait(p, depth, func(pcs []uintptr) string {
			if depth == 1 {
				recursivePC = pcs[0]
				recursive = slices.Clone(pcs)
				return "worker"
			}
			flat = slices.Clone(pcs)
			return fmt.Sprintf("worker %x", recursivePC)
		})
	}
	if len(recursive) != len(flat)+1 || !slices.Equal(recursive[1:], flat) {
		t.Fatal("profile fixture must differ by exactly one leading return address")
	}
	decoded := profDecode(t, profWrite(t, p))
	if len(decoded.samples) != 2 {
		t.Fatalf("distinct task kinds and stacks merged into %d samples; want 2", len(decoded.samples))
	}
	labels := map[string]bool{}
	for _, sample := range decoded.samples {
		for _, label := range sample.labels {
			if label.key == "task" {
				labels[label.str] = true
			}
		}
	}
	for _, name := range []string{"worker", fmt.Sprintf("worker %x", recursivePC)} {
		if !labels[name] {
			t.Errorf("profile omitted task kind %q", name)
		}
	}
}
