package qobs_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

var errInsufficient = errors.New("insufficient funds")

// record is one step as a service records it: a few arguments, an
// observation, an outcome.
func record(ctx context.Context, i int) {
	var err error
	if i%4 == 0 {
		err = errInsufficient
	}
	qobs.Record(ctx, "transfer", err, "src", "alice", "dest", "bob", "amount", i, "obs.balance", -75)
}

// BenchmarkRecord is a step recorded through the context's recorder to a
// step log written to io.Discard: the cost of step telemetry itself.
// Memory is the recording without the encoding; Global is a step with
// context.Background() found through the global recorder; Parallel
// records from every core, through one recorder (run with -cpu 1,4,8).
func BenchmarkRecord(b *testing.B) {
	sinks := []struct {
		name string
		sink func() qobs.Sink
	}{
		{"Memory", func() qobs.Sink { return discardSink{} }},
		{"PerStep", func() qobs.Sink { return qobs.NewJSONLSink(io.Discard, qobs.PerStep) }},
		{"Buffered", func() qobs.Sink { return qobs.NewJSONLSink(io.Discard, qobs.Buffered) }},
	}
	for _, s := range sinks {
		b.Run(s.name, func(b *testing.B) {
			rec := qobs.NewRecorder(s.sink(), qobs.WithActor("bank"))
			ctx := qobs.WithRecorder(context.Background(), rec)
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				record(ctx, i)
				i++
			}
		})
	}
	b.Run("Global", func(b *testing.B) {
		rec := qobs.NewRecorder(qobs.NewJSONLSink(io.Discard, qobs.Buffered), qobs.WithActor("bank"))
		restore, err := qobs.InstallGlobal(rec)
		if err != nil {
			b.Fatal(err)
		}
		defer restore()
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			record(context.Background(), i)
			i++
		}
	})
	b.Run("Thread", func(b *testing.B) {
		rec := qobs.NewRecorder(qobs.NewJSONLSink(io.Discard, qobs.Buffered), qobs.WithActor("bank"))
		ctx := qobs.Thread(qobs.WithRecorder(context.Background(), rec))
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			record(ctx, i)
			i++
		}
	})
	b.Run("Parallel", func(b *testing.B) {
		rec := qobs.NewRecorder(qobs.NewJSONLSink(io.Discard, qobs.Buffered), qobs.WithActor("bank"))
		ctx := qobs.WithRecorder(context.Background(), rec)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				record(ctx, i)
				i++
			}
		})
	})
}

// discardSink drops every step: the recorder's own cost.
type discardSink struct{}

func (discardSink) Record(context.Context, *qtrace.Step) {}
