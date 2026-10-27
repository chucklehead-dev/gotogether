package otelio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type ambiguous struct{}

func (ambiguous) Error() string        { return "timeout" }
func (ambiguous) QuintOutcome() string { return "ambiguous" }

func record(r *qobs.Recorder) {
	ctx := qobs.Thread(context.Background())
	r.Record(ctx, "pushStart", nil, "payload", "req-1", "batch", uint64(7), "order.batch", uint64(7), "process", "e1", "actor", "w/traces")
	r.Record(ctx, "writeTable", ambiguous{}, "batch", 7, "obs.rows", 3)
	r.Record(ctx, "writeParquet", errors.New("boom"), "when", "ok", "batch", 7) // skipped
}

func TestInMemorySpans(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	record(qobs.NewRecorder(qobs.NewOTelSink(tp)))
	steps, err := StepsFromSpans(sr.Ended())
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
	a, b := steps[0], steps[1]
	if a.Action != "pushStart" || a.Args["batch"] != int64(7) || a.Args["payload"] != "req-1" || a.Order["batch"] != 7 ||
		a.Process != "e1" || a.Actor != "w/traces" || a.Seq != 1 || a.Thread == "" {
		t.Fatalf("step 1: %+v", a)
	}
	if b.Outcome != "ambiguous" || b.Error != "timeout" || b.Obs["rows"] != int64(3) || b.Thread != a.Thread || b.Seq != 2 {
		t.Fatalf("step 2: %+v", b)
	}
}

func TestOTLPJSONFile(t *testing.T) {
	var buf bytes.Buffer
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(NewFileExporter(&buf)))
	record(qobs.NewRecorder(qobs.NewOTelSink(tp)))
	_ = tp.Shutdown(context.Background())
	steps, err := ReadSteps(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || steps[1].Outcome != "ambiguous" || steps[0].Args["batch"] != int64(7) {
		t.Fatalf("steps: %+v", steps)
	}
}

// Overhead of one recorded step, by sink.
func BenchmarkRecordDisabled(b *testing.B) {
	qobs.SetGlobal(nil)
	ctx := context.Background()
	for i := 0; b.Loop(); i++ {
		qobs.Record(ctx, "writeTable", nil, "payload", "req-1", "batch", uint64(i), "gen", "g20260924T100000")
	}
}

func BenchmarkRecordMemory(b *testing.B) {
	r := qobs.NewRecorder(&qobs.MemorySink{})
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		r.Record(ctx, "writeTable", nil, "payload", "req-1", "batch", uint64(i), "gen", "g20260924T100000")
	}
}

func BenchmarkRecordOTelBatchToFile(b *testing.B) {
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(NewFileExporter(io.Discard)))
	defer tp.Shutdown(context.Background())
	r := qobs.NewRecorder(qobs.NewOTelSink(tp))
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		r.Record(ctx, "writeTable", nil, "payload", "req-1", "batch", uint64(i), "gen", "g20260924T100000")
	}
}

func BenchmarkRecordStepLog(b *testing.B) {
	r := qobs.NewRecorder(qobs.NewJSONLSink(io.Discard, qobs.PerStep))
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		r.Record(ctx, "writeTable", nil, "payload", "req-1", "batch", uint64(i), "gen", "g20260924T100000")
	}
}

// A step's ref travels as the event's quint.ref, through the SDK's spans
// and an OTLP/JSON file alike.
func TestRefRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr), sdktrace.WithSyncer(NewFileExporter(&buf)))
	qobs.NewRecorder(qobs.NewOTelSink(tp)).Record(context.Background(), "submit", nil, qobs.Ref("trace.jsonl:42"), "job", "j1")
	_ = tp.Shutdown(context.Background())
	mem, err := StepsFromSpans(sr.Ended())
	if err != nil {
		t.Fatal(err)
	}
	file, err := ReadSteps(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for _, steps := range [][]qtrace.Step{mem, file} {
		if len(steps) != 1 || steps[0].Ref != "trace.jsonl:42" || steps[0].Args["job"] != "j1" {
			t.Errorf("steps: %+v", steps)
		}
	}
}

// A recorder's watermark (Recorder.CompleteThrough) survives OTel, in
// memory and in an OTLP/JSON file: a record, not a step, with no seq of
// its own, so the recorder's next step keeps the sequence dense.
func TestWatermarkRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr), sdktrace.WithSyncer(NewFileExporter(&buf)))
	r := qobs.NewRecorder(qobs.NewOTelSink(tp))
	through := time.Unix(1_800_000_000, 5)
	r.Record(context.Background(), "submit", nil, "job", "j1")
	r.CompleteThrough(context.Background(), through)
	r.Record(context.Background(), "submit", nil, "job", "j2")
	_ = tp.Shutdown(context.Background())
	mem, err := StepsFromSpans(sr.Ended())
	if err != nil {
		t.Fatal(err)
	}
	file, err := ReadSteps(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for _, steps := range [][]qtrace.Step{mem, file} {
		trs, issues := qtrace.Reconstruct(steps)
		if len(trs) != 1 || len(trs[0].Steps) != 2 || len(issues) > 0 || len(trs[0].Issues) > 0 {
			t.Fatalf("steps %+v: %v %v", steps, trs, issues)
		}
		if got, every := trs[0].CompleteThrough(); !got.Equal(through) || !every {
			t.Errorf("complete through %v (every %v), want %v", got, every, through)
		}
	}
}
