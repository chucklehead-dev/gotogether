package qtrace

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"
)

// benchStep is a step as a service records it: an action, its actor and
// thread, a few arguments and observations.
func benchStep(i int) Step {
	return Step{
		Action: "transfer", Seq: uint64(i + 1), Recorder: "a1b2c3d4e5f6", Actor: "bank", Process: "p1", Thread: "t3",
		Outcome: "ok", Time: time.Unix(1_700_000_000, int64(i)*1000),
		Args: map[string]any{"src": "alice", "dest": "bob", "amount": int64(25)},
		Obs:  map[string]any{"balance": int64(-75), "ok": true},
	}
}

// BenchmarkMarshalLine is one step encoded as a step-log line: what
// every qobs.JSONLSink step costs before its write.
func BenchmarkMarshalLine(b *testing.B) {
	s := benchStep(1)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.MarshalLine(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkReadJSONL reads a log of 10,000 steps.
func BenchmarkReadJSONL(b *testing.B) {
	steps := make([]Step, 10_000)
	for i := range steps {
		steps[i] = benchStep(i)
	}
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, steps); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(buf.Len()))
	for b.Loop() {
		if _, err := ReadJSONL(bytes.NewReader(buf.Bytes())); err != nil {
			b.Fatal(err)
		}
	}
}

// reconstructInput is n steps of 8 recorders and 4 actors, each recorder
// its own thread, shuffled as collectors deliver them; with lamport, each
// step carries a domain clock too.
func reconstructInput(n int, lamport bool) []Step {
	r := rand.New(rand.NewPCG(1, 2))
	steps := make([]Step, n)
	for i := range steps {
		rec := i % 8
		s := benchStep(i)
		s.Recorder, s.Seq = fmt.Sprintf("rec%d", rec), uint64(i/8+1)
		s.Actor, s.Process, s.Thread = fmt.Sprintf("actor%d", rec%4), fmt.Sprintf("p%d", rec), "t"
		if lamport {
			s.Order = map[string]int64{"lamport": int64(i)}
		}
		steps[i] = s
	}
	r.Shuffle(len(steps), func(i, j int) { steps[i], steps[j] = steps[j], steps[i] })
	return steps
}

// BenchmarkReconstruct orders shuffled steps into traces: what
// `gotogether validate` does first with a production log.
func BenchmarkReconstruct(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		for _, lamport := range []bool{false, true} {
			if n == 100_000 && testing.Short() {
				continue
			}
			b.Run(fmt.Sprintf("steps=%d/lamport=%v", n, lamport), func(b *testing.B) {
				steps := reconstructInput(n, lamport)
				b.ReportAllocs()
				for b.Loop() {
					Reconstruct(steps)
				}
			})
		}
	}
}
