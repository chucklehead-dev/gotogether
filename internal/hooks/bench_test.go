package hooks

import "testing"

// BenchmarkGoroutineID is the goroutine lookup every woven sync call, a
// qobs.Thread's step and a step without a recorder in its context pay;
// Parallel is it on every core at once.
func BenchmarkGoroutineID(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		GoroutineID()
	}
}

func BenchmarkGoroutineIDParallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			GoroutineID()
		}
	})
}
