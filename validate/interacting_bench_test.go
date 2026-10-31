package validate

import (
	"fmt"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// BenchmarkInteractingValidation measures the warm, passing Go validation
// path separately from qeval's transition/invariant microbenchmarks. Transfers
// join accounts, so the log must be checked whole rather than partitioned.
func BenchmarkInteractingValidation(b *testing.B) {
	skip.Quint(b)
	// Measure the evaluator path, not the random cost of a Quint shadow audit.
	b.Setenv("GOTOGETHER_EVAL_SAMPLE", "0")
	for _, entities := range []int{10, 100} {
		for _, transfers := range []int{100, 1000} {
			b.Run(fmt.Sprintf("entities=%d/transfers=%d", entities, transfers), func(b *testing.B) {
				var ops [][]any
				for i := range entities {
					acct := fmt.Sprintf("acct-%d", i)
					ops = append(ops, op("open", "acct", acct), op("deposit", "acct", acct, "amount", 10))
				}
				for i := range transfers {
					a, z := "acct-0", "acct-1"
					if i%2 == 1 {
						a, z = z, a
					}
					ops = append(ops, op("transfer", "acct", a, "dest", z, "amount", 1))
				}
				steps := ledgerLog(ops...)
				c, err := NewChecker("testdata/ledger_whole.yaml", Options{Dir: b.TempDir(), NoITF: true})
				if err != nil {
					b.Fatal(err)
				}
				check := func() {
					rep, err := c.Check(b.Context(), steps)
					if err != nil {
						b.Fatal(err)
					}
					if !rep.OK() || len(rep.Actors) != 1 {
						b.Fatal("expected one passing whole-log actor: ", rep.Summary())
					}
				}
				check() // Compile and warm caches before timing.
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					check()
				}
			})
		}
	}
}
