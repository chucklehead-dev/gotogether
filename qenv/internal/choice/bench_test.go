package choice

import "testing"

// BenchmarkSeededFault is a fault choice at Rates with a few kinds, the
// choice the scheduler makes for each nemesis family at every step.
func BenchmarkSeededFault(b *testing.B) {
	s := NewSeeded(1)
	s.Rates = Rates{Default: 0.05, Kinds: map[string]float64{"nemesis": 0.005, "rpc.api": 0.1, "store.db": 0.02, "queue.jobs.ack": 0.1}}
	for b.Loop() {
		s.Choose("fault.nemesis.crash", 2)
	}
}

// BenchmarkSeededSchedule is a schedule choice: which runnable task runs.
func BenchmarkSeededSchedule(b *testing.B) {
	s := NewSeeded(1)
	for b.Loop() {
		s.Choose("schedule", 4)
	}
}

// BenchmarkSeededFaultDefault is a fault choice at DefaultRates, an
// exploration's.
func BenchmarkSeededFaultDefault(b *testing.B) {
	s := NewSeeded(1)
	for b.Loop() {
		s.Choose("fault.rpc.payments.request", 3)
	}
}
