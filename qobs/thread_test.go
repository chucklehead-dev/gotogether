package qobs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
)

// A Thread asserts program order: steps of one goroutine. Recording it from
// a second goroutine is an issue, found as it records.
func TestThreadFromTwoGoroutines(t *testing.T) {
	r := NewRecorder(&MemorySink{})
	ctx := Thread(context.Background())
	r.Record(ctx, "a", nil)
	r.Record(ctx, "b", nil)
	if len(r.Issues()) != 0 {
		t.Fatalf("one goroutine: %v", r.Issues())
	}
	done := make(chan struct{})
	go func() { defer close(done); r.Record(ctx, "c", nil) }()
	<-done
	if is := r.Issues(); len(is) != 1 || !strings.Contains(is[0], `recorded "c" from goroutine`) || !strings.Contains(is[0], "give each its own") {
		t.Errorf("two goroutines: %v", is)
	}
}

// Excluded calls do not assert program order, and must not claim a thread or
// replace its current owner. A later accepted misuse still must be diagnosed.
func TestThreadFilteredCallsDoNotClaimOwnership(t *testing.T) {
	for _, first := range []bool{false, true} {
		t.Run(map[bool]string{false: "unowned", true: "owned"}[first], func(t *testing.T) {
			mem := &MemorySink{}
			r := NewRecorder(mem)
			ctx := Thread(t.Context())
			if first {
				r.Record(ctx, "first", nil)
			}
			done := make(chan struct{})
			go func() { defer close(done); r.Record(ctx, "filtered", errors.New("excluded"), "when", "ok") }()
			<-done
			r.Record(ctx, "kept", nil)
			if issues := r.Issues(); len(issues) != 0 {
				t.Fatalf("excluded call changed ownership: %v", issues)
			}
			want := 1
			if first {
				want++
			}
			if len(mem.Steps()) != want {
				t.Fatalf("steps = %v", mem.Steps())
			}
			done = make(chan struct{})
			go func() { defer close(done); r.Record(ctx, "misused", nil) }()
			<-done
			if issues := r.Issues(); len(issues) != 1 || !strings.Contains(issues[0], `recorded "misused"`) {
				t.Fatalf("accepted misuse = %v", issues)
			}
		})
	}
}

// Discarded unwind work is diagnostic evidence, not part of the checked trace.
func TestThreadDiscardedCallsDoNotClaimOwnership(t *testing.T) {
	restore := hooks.SetSite(func(context.Context, int64) hooks.Site { return hooks.Site{Discard: true} })
	t.Cleanup(restore)
	r := NewRecorder(&MemorySink{})
	ctx := Thread(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); r.Record(ctx, "interrupted", nil) }()
	<-done
	restore()
	r.Record(ctx, "kept", nil)
	if issues := r.Issues(); len(issues) != 0 {
		t.Fatalf("discarded ownership: %v", issues)
	}
	if got := r.Interrupted(); len(got) != 1 || got[0].Action != "interrupted" {
		t.Fatalf("interrupted = %v", got)
	}
}

func TestThreadOverrideDoesNotValidateUnusedContextIdentity(t *testing.T) {
	for _, override := range []string{"other", ""} {
		t.Run(override, func(t *testing.T) {
			mem := &MemorySink{}
			r := NewRecorder(mem)
			ctx := Thread(t.Context())
			r.Record(ctx, "first", nil)
			done := make(chan struct{})
			go func() { defer close(done); r.Record(ctx, "overridden", nil, "thread", override) }()
			<-done
			r.Record(ctx, "kept", nil)
			if issues := r.Issues(); len(issues) != 0 {
				t.Fatalf("unused context thread: %v", issues)
			}
			if got := mem.Steps(); len(got) != 3 || got[1].Thread != override {
				t.Fatalf("steps = %v", got)
			}
		})
	}
}

// An invocation is kept regardless of when, and Done completes it regardless
// of when. Both therefore retain ownership checking for the emitted records.
func TestThreadInvokeAndDoneStillCheckOwnership(t *testing.T) {
	for _, kind := range []string{"invoke", "done"} {
		t.Run(kind, func(t *testing.T) {
			r := NewRecorder(&MemorySink{})
			ctx := Thread(t.Context())
			r.Record(ctx, "first", nil)
			var call *Call
			if kind == "done" {
				call = r.Invoke(ctx, "operation", "when", "error")
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				if kind == "invoke" {
					r.Invoke(ctx, "operation", "when", "error")
				} else {
					call.Done(nil, "when", "error")
				}
			}()
			<-done
			if issues := r.Issues(); len(issues) != 1 || !strings.Contains(issues[0], `recorded "operation"`) {
				t.Fatalf("%s misuse = %v", kind, issues)
			}
		})
	}
}
