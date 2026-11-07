package qeval

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkFoldSumDefinedness measures the complete evaluation of a safe sum,
// including set construction and the proof that every fold order is defined.
// Quint compilation happens once, outside both timed subbenchmarks.
func BenchmarkFoldSumDefinedness(b *testing.B) {
	path := filepath.Join(b.TempDir(), "foldSumBenchmark.qnt")
	const source = `module foldSumBenchmark {
  pure def sum(n: int): int = 1.to(n).fold(0, (a, x) => a + x)
  var out: int
  action init = out' = 0
  action step = out' = out
}`
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		b.Fatal(err)
	}
	m := compileFile(b, path, "foldSumBenchmark")
	for _, size := range []int{20, 1000} {
		b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
			want := Int(size * (size + 1) / 2)
			if got, err := m.Eval(nil, nil, "sum", Int(size)); err != nil || got != want {
				b.Fatalf("sum(%d) = %v, %v; want %v", size, got, err, want)
			}
			b.ReportAllocs()
			for b.Loop() {
				if got, err := m.Eval(nil, nil, "sum", Int(size)); err != nil || got != want {
					b.Fatalf("sum(%d) = %v, %v; want %v", size, got, err, want)
				}
			}
		})
	}
}
