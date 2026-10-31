package qeval

import "testing"

// BenchmarkLazyLetErrors separates ordinary binding from the rare deferred
// error paths. An unused failing let still returns its body's value, while
// reading that binding must preserve the model's runtime error.
func BenchmarkLazyLetErrors(b *testing.B) {
	m := compile(b, "env.qnt", "env", "init", "step")
	for _, tc := range []struct {
		name string
		want Int
		bad  bool
	}{
		{name: "successfulLet", want: 1},
		{name: "unusedError", want: 7},
		{name: "referencedError", bad: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				v, err := m.Eval(nil, nil, tc.name, Int(0))
				if tc.bad {
					if _, ok := err.(*EvalError); !ok || v != nil {
						b.Fatalf("want model runtime error, got %v, %T: %v", v, err, err)
					}
				} else if err != nil || v != tc.want {
					b.Fatalf("want %v, got %v: %v", tc.want, v, err)
				}
			}
		})
	}
}
