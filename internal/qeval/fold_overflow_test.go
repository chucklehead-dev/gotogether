package qeval

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// The same final mathematical sum can have an overflowing prefix in one set
// order. A refusal is required even if Go's canonical order happens to work.
func TestFoldIntegerDefinedness(t *testing.T) {
	m := compile(t, "fold_overflow.qnt", "foldOverflow", "init", "step")
	for _, name := range []string{"nightly", "subtract", "unsafeLarge", "unsafeSubtractLarge", "unsafeConditional", "unsafeNested", "unsafeNestedOrder", "unsafeNestedOrderLarge"} {
		t.Run(name, func(t *testing.T) {
			v, err := m.Eval(nil, nil, name)
			var unsupported *Unsupported
			if !errors.As(err, &unsupported) {
				t.Fatalf("order-dependent integer definedness returned %s, %v; want Unsupported", String(v), err)
			}
		})
	}
	for name, want := range map[string]Int{
		"safeLarge": 210, "safeMixed": 0, "safeConditional": 10,
		"safeSubtract": 790, "safeRightAcc": -790,
		"safeNestedMax": math.MaxInt64 - 20, "safeNestedMin": math.MinInt64 + 20,
		"safeConditionalMax": math.MaxInt64 - 190,
		"safeBoundaryMax":    math.MaxInt64, "safeBoundaryMin": math.MinInt64,
		"safeWideDelta": math.MaxInt64 - 1, "safeWideSubtract": -math.MaxInt64,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := m.Eval(nil, nil, name)
			if err != nil || got != want {
				t.Fatalf("safe large fold: got %s, %v; want %s", String(got), err, String(want))
			}
		})
	}
	for _, name := range []string{"independentError", "independentBranchError"} {
		t.Run(name, func(t *testing.T) {
			_, err := m.Eval(nil, nil, name)
			var modelError *EvalError
			if !errors.As(err, &modelError) {
				t.Fatalf("an accumulator-independent error became %v; want EvalError", err)
			}
		})
	}
}

// Native backends need not choose the same set order or intermediate integer
// semantics. Go must refuse the unsafe expression, while the safe controls
// must agree with both complete native results.
func TestFoldOverflowNativeBackends(t *testing.T) {
	skip.Quint(t)
	cases := []struct{ expr, want string }{
		{"Set((-9223372036854775807), 10).fold(9223372036854775807, (a, x) => a + x)", "refused"},
		{"Set((-9223372036854775807), 10).fold((-9223372036854775807), (a, x) => a - x)", "refused"},
		{"1.to(20).fold(0, (a, x) => a + x)", "value 210"},
		{"(-20).to(20).fold(0, (a, x) => a + x)", "value 0"},
		{"1.to(20).fold(0, (a, x) => if (x % 2 == 0) a + x else a - x)", "value 10"},
		{"1.to(20).fold(9223372036854775807 - 20, (a, x) => (a + x) - x)", "value 9223372036854775787"},
		{"1.to(20).fold((-9223372036854775807) - 1 + 20, (a, x) => (a - x) + x)", "value -9223372036854775788"},
		{"1.to(20).fold(1000, (a, x) => a - x)", "value 790"},
		{"1.to(20).fold((-1000), (a, x) => x + a)", "value -790"},
		{"1.to(20).fold(0, (a, x) => a + (1 / (x - x)))", "error"},
		{"Set(-1, 1).fold(9223372036854775807 - 1, (a, x) => if (x < 0) (a + 1) - 1 else a + 1)", "refused"},
		{"(-10).to(10).fold(9223372036854775807 - 1, (a, x) => if (x < 0) (a + 1) - 1 else if (x == 1) a + 1 else a)", "refused"},
		{"1.to(20).fold(9223372036854775807 - 210, (a, x) => a + x)", "value 9223372036854775807"},
		{"1.to(20).fold((-9223372036854775807) - 1 + 210, (a, x) => a - x)", "value -9223372036854775808"},
		{"1.to(20).fold((-9223372036854775807) - 1, (a, x) => if (x == 1) (a + 9223372036854775807) + 9223372036854775807 else a)", "value 9223372036854775806"},
		{"1.to(20).fold(9223372036854775807, (a, x) => if (x == 1) (a - 9223372036854775807) - 9223372036854775807 else a)", "value -9223372036854775807"},
	}
	entries := make([]gentry, len(cases))
	for i, c := range cases {
		entries[i] = gentry{t: &gtyp{k: kInt}, expr: &gnode{op: "lit", lit: c.expr}}
	}
	b := &gbatch{entries: entries, drop: map[int]bool{}}
	q, qs, err := b.run(t.Context(), t.TempDir(), 1, backends, func(e gentry, msg string) { t.Errorf("ill-typed %s: %s", e.root(), msg) })
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		if b.drop[i] {
			t.Fatalf("native control unexpectedly dropped: %s", c.expr)
		}
		if got := q[i].String(); !strings.HasPrefix(got, c.want) {
			t.Errorf("Go %s: got %s, want %s", c.expr, got, c.want)
		}
		for _, backend := range backends {
			got := qs[backend][i]
			t.Logf("%s entry %d: %s", backend, i, got.String())
			if got.missing {
				t.Fatalf("missing native outcome for %s entry %d", backend, i)
			}
			if c.want != "refused" {
				if d, _ := judge(b, q, qs, i, backend); d != nil {
					t.Errorf("safe fold disagreed with %s: %+v", backend, d)
				}
			}
		}
	}
	if got := qs["rust"][0]; !strings.Contains(strings.ToLower(got.err), "overflow") {
		t.Fatalf("nightly Rust overflow was not reproduced: %s", got.String())
	}
}
