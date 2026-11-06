package qhegel_test

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qtest"
	"hegel.dev/go/hegel"
)

type violationTB struct {
	*fatalTB
	logs strings.Builder
}

func (f *violationTB) Logf(format string, args ...any) {
	fmt.Fprintf(&f.logs, format, args...)
	f.TB.Logf(format, args...)
}

// An earlier model-rejected candidate cannot establish a reproduced violation
// when final replay conforms or fails operationally instead.
func TestViolationCandidateRequiresReproduction(t *testing.T) {
	needQuint(t)
	t.Setenv("GOTOGETHER_VERBOSE", "")
	for _, mode := range []string{"stable", "first-only", "operational-after", "inconclusive"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int64
			f := &violationTB{fatalTB: &fatalTB{TB: t}}
			var result *qhegel.Violation
			done := make(chan struct{})
			go func() { //qenv:ok native test adapter uses Goexit to record Fatalf
				defer close(done)
				binding := "testdata/bank.yaml"
				if mode == "inconclusive" {
					binding = "../validate/testdata/ticks.yaml"
				}
				result = qhegel.FindViolation(f, qhegel.ViolationConfig{Binding: binding}, func(tc hegel.TestCase, c *qhegel.Case) {
					_ = hegel.Draw(tc, hegel.Integers(0, 10))
					call := calls.Add(1)
					if mode == "inconclusive" {
						c.Step("fire", nil)
						return
					}
					amount := 0 // bank deposit's amount > 0 guard rejects this step
					if mode != "stable" && call > 1 {
						amount = 1
					}
					if mode == "operational-after" && call > 1 {
						tc.Errorf("operational-after-candidate sentinel")
					}
					c.Step("deposit", nil, "account", "alice", "amount", amount, "obs.alice", amount, "obs.bob", 0)
				}, hegel.WithTestCases(3), hegel.WithSeed(1), hegel.WithDatabase(""), hegel.WithReproductionBlob(false))
			}()
			<-done
			t.Logf("mode=%s calls=%d returned=%t fatal=%q", mode, calls.Load(), result != nil, f.msg)
			if mode == "stable" {
				if result == nil || f.msg != "" {
					t.Fatalf("deterministic violation was not returned: %q", f.msg)
				}
				result.Expect(t, qtest.Rejects("deposit"))
				return
			}
			diagnostic := map[string]string{"first-only": "unconfirmed failure", "operational-after": "operational-after-candidate sentinel", "inconclusive": "conclusive verdict"}[mode]
			if !strings.Contains(f.logs.String(), diagnostic) {
				t.Errorf("search diagnostic %q missing from captured output: %s", diagnostic, f.logs.String())
			}
			if calls.Load() < 2 {
				t.Fatal("negative control did not exercise replay")
			}
			if result != nil || !strings.Contains(f.msg, "test failed:") {
				t.Fatalf("earlier candidate manufactured a completed shrunk violation: returned=%t fatal=%q", result != nil, f.msg)
			}
		})
	}
}
