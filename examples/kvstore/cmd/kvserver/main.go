// Command kvserver runs the kvstore example service, recording what it does
// for checking against model/kv.qnt:
//
//	kvserver -addr :8080 -steps steps.jsonl -spans spans.jsonl
//
// -steps writes the store's model steps (qobs, native step log); -spans
// writes the server's ordinary OTel spans as OTLP/JSON, as a collector's
// file exporter would. Stop it with SIGINT/SIGTERM to flush both, then
//
//	gotogether validate -binding kv.binding.yaml steps.jsonl
//	gotogether validate -binding kv.binding.yaml -convert kv.convert.yaml spans.jsonl
//
// -rotate N rotates the step log every N operations: steps.1.jsonl,
// steps.2.jsonl, ..., each after the first starting with the store's
// whole state (the model's restore, the binding's snapshot), so a later
// file is checked alone:
//
//	kvserver -steps steps.jsonl -rotate 100
//	gotogether validate -binding kv.binding.yaml steps.3.jsonl
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/chucklehead-dev/gotogether/examples/kvstore/kv"
	"github.com/chucklehead-dev/gotogether/otelio"
	"github.com/chucklehead-dev/gotogether/qobs"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	steps := flag.String("steps", "", "write model steps (native step log) here")
	spans := flag.String("spans", "", "write OTel spans (OTLP/JSON) here")
	mutation := flag.String("mutation", "", "inject a bug: stale-cas | revision-on-conflict | stale-read | late-revision-on-conflict | stale-snapshot")
	rotate := flag.Int("rotate", 0, "with -steps: start a new step log (steps.1.jsonl, steps.2.jsonl, ...) every N operations, each headed by a snapshot")
	flag.Parse()

	m := kv.Faithful
	switch *mutation {
	case "":
	case "stale-cas":
		m = kv.StaleCAS
	case "revision-on-conflict":
		m = kv.RevisionOnConflict
	case "stale-read":
		m = kv.StaleRead
	case "late-revision-on-conflict":
		m = kv.LateRevisionOnConflict
	case "stale-snapshot":
		m = kv.StaleSnapshot
	default:
		log.Fatalf("unknown mutation %q", *mutation)
	}

	store := kv.NewStore(m)
	switch {
	case *steps != "" && *rotate > 0:
		rl, err := kv.NewRotatingLog(*steps)
		if err != nil {
			log.Fatal(err)
		}
		defer func() {
			_ = rl.Close()
			if err := store.Err(); err != nil {
				log.Printf("rotation stopped: %v", err)
			}
			log.Printf("step logs: %v", rl.Files())
		}()
		store.RotateEvery(*rotate, rl.Rotate)
		qobs.SetGlobal(qobs.NewRecorder(rl, qobs.WithActor("kv"), qobs.WithProcess("kvserver")))
	case *steps != "":
		sink, err := qobs.FileSink(*steps, qobs.PerStep)
		if err != nil {
			log.Fatal(err)
		}
		defer sink.Close()
		qobs.SetGlobal(qobs.NewRecorder(sink, qobs.WithActor("kv"), qobs.WithProcess("kvserver")))
	case *rotate > 0:
		log.Fatal("-rotate rotates the step log: give -steps")
	}
	var tp trace.TracerProvider
	if *spans != "" {
		f, err := os.Create(*spans)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		sdk := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(otelio.NewFileExporter(f)),
			sdktrace.WithSampler(sdktrace.AlwaysSample()), // a sampled-out span is a step lost
			sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "kvserver"))))
		defer sdk.Shutdown(context.Background())
		tp = sdk
	}

	srv := &http.Server{Handler: kv.NewServer(store, tp).Handler()}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("kvserver listening on %s (mutation %q)", ln.Addr(), *mutation)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	log.Print("kvserver stopped; flushing")
}
