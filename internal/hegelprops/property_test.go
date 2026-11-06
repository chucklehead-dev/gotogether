package hegelprops

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"hegel.dev/go/hegel"
)

// checkProperty keeps native run errors visible. For native run failures such
// as health checks, hegel.Test can mark failure without reporting the error.
// Run writes replay diagnostics to stdout; its public API has no output option.
func checkProperty(t testing.TB, body func(hegel.TestCase), opts ...hegel.Option) {
	t.Helper()
	// Run has no public per-test database key. Isolate each property's corpus;
	// explicit caller options still override this default.
	database := filepath.Join(".hegel", "properties", fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name()))))
	if os.Getenv("CI") != "" {
		database = ""
	}
	options := append([]hegel.Option{hegel.WithDatabase(database)}, opts...)
	if testing.Short() {
		options = append(options, hegel.WithTestCases(10))
	}
	if err := hegel.Run(body, options...); err != nil {
		t.Errorf("Hegel property failed: %v", err)
	}
}

type propertyTB struct {
	testing.TB
	errors []string
}

func (t *propertyTB) Errorf(format string, args ...any) {
	t.errors = append(t.errors, fmt.Sprintf(format, args...))
}

func TestPropertyReportsNativeRunErrors(t *testing.T) {
	tests := []struct {
		name, diagnostic string
		body             func(hegel.TestCase)
	}{
		{"healthcheck", "FailedHealthCheck: FilterTooMuch", func(tc hegel.TestCase) { _ = hegel.Draw(tc, hegel.Booleans()); tc.Assume(false) }},
		{"assertion", "property test failed", func(tc hegel.TestCase) { tc.Errorf("oracle assertion sentinel") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := &propertyTB{TB: t}
			checkProperty(target, test.body, hegel.WithDatabase(""), hegel.WithSeed(1), hegel.WithTestCases(60), hegel.WithReproductionBlob(false))
			if len(target.errors) != 1 || !strings.Contains(target.errors[0], test.diagnostic) {
				t.Fatalf("missing native diagnostic %q: %v", test.diagnostic, target.errors)
			}
		})
	}
}

func TestPropertyDoesNotClaimUnreproducedFailurePasses(t *testing.T) {
	target := &propertyTB{TB: t}
	var calls atomic.Int64
	checkProperty(target, func(tc hegel.TestCase) {
		_ = hegel.Draw(tc, hegel.Integers(0, 10))
		if calls.Add(1) == 1 {
			tc.Errorf("first-only failure")
		}
	}, hegel.WithDatabase(""), hegel.WithSeed(1), hegel.WithTestCases(3), hegel.WithReproductionBlob(false))
	if calls.Load() < 2 || len(target.errors) != 1 {
		t.Fatalf("unreproduced failure lost: calls=%d errors=%v", calls.Load(), target.errors)
	}
}

func TestPropertyNativeControlFlow(t *testing.T) {
	t.Run("passes", func(t *testing.T) {
		target := &propertyTB{TB: t}
		var calls atomic.Int64
		checkProperty(target, func(tc hegel.TestCase) { _ = hegel.Draw(tc, hegel.Booleans()); calls.Add(1) }, hegel.WithDatabase(""), hegel.WithTestCases(3))
		if len(target.errors) != 0 || calls.Load() == 0 {
			t.Fatalf("conforming property: calls=%d errors=%v", calls.Load(), target.errors)
		}
	})
	t.Run("panic", func(t *testing.T) {
		target := &propertyTB{TB: t}
		sentinel := new(int)
		defer func() {
			if got := recover(); got != sentinel {
				t.Errorf("user panic changed: %v", got)
			}
			if len(target.errors) != 0 {
				t.Errorf("panic became run error: %v", target.errors)
			}
		}()
		checkProperty(target, func(hegel.TestCase) { panic(sentinel) }, hegel.WithDatabase(""), hegel.WithTestCases(3), hegel.WithReproductionBlob(false))
	})
	t.Run("Goexit", func(t *testing.T) {
		target := &propertyTB{TB: t}
		done := make(chan struct{})
		var returned atomic.Bool
		go func() { //qenv:ok native control verifies Goexit propagation outside simulation
			defer close(done)
			checkProperty(target, func(hegel.TestCase) { runtime.Goexit() }, hegel.WithDatabase(""), hegel.WithTestCases(3))
			returned.Store(true)
		}()
		<-done
		if returned.Load() || len(target.errors) != 0 {
			t.Fatalf("Goexit changed: returned=%t errors=%v", returned.Load(), target.errors)
		}
	})
}

func TestPropertyShortBudgetOverridesCaller(t *testing.T) {
	target := &propertyTB{TB: t}
	var calls atomic.Int64
	checkProperty(target, func(tc hegel.TestCase) { _ = hegel.Draw(tc, hegel.Integers(0, 1000000)); calls.Add(1) }, hegel.WithDatabase(""), hegel.WithSeed(1), hegel.WithTestCases(17))
	want := int64(17)
	if testing.Short() {
		want = 10
	}
	if calls.Load() != want || len(target.errors) != 0 {
		t.Fatalf("case budget: got %d, want %d; errors=%v", calls.Load(), want, target.errors)
	}
}
