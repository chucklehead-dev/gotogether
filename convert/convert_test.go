package convert

import (
	"context"
	"testing"

	"github.com/chucklehead-dev/gotogether/otelio"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const rules = `
actor: resource:service.name
rules:
  - span: "Bank/Health*"
    drop: true
  - span: "Bank/Deposit"
    action: deposit
    args: { account: attr:bank.account, amount: attr:bank.amount|int }
    obs: { balance: attr:bank.balance }
  - span: "Bank/Withdraw"
    action: withdraw
    args: { account: attr:bank.account, amount: attr:bank.amount }
  - span: "Bank/Audit"
    event: audited
    action: audit
    args: { n: event:count }
`

// An application's own spans, with no quint instrumentation, become steps.
func TestConvertAppSpans(t *testing.T) {
	r, err := Parse([]byte(rules))
	if err != nil {
		t.Fatal(err)
	}
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr),
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "bank"), attribute.String("service.instance.id", "i-1"))))
	tr := tp.Tracer("app")
	ctx := context.Background()
	_, s := tr.Start(ctx, "Bank/Deposit")
	s.SetAttributes(attribute.String("bank.account", "alice"), attribute.String("bank.amount", "5"), attribute.Int64("bank.balance", 5))
	s.End()
	_, s = tr.Start(ctx, "Bank/HealthCheck")
	s.End()
	_, s = tr.Start(ctx, "Bank/Withdraw")
	s.SetAttributes(attribute.String("bank.account", "alice"), attribute.Int64("bank.amount", 9))
	s.SetStatus(codes.Error, "insufficient funds")
	s.End()
	_, s = tr.Start(ctx, "Bank/Audit")
	s.AddEvent("audited", traceAttrs(attribute.Int64("count", 2)))
	s.AddEvent("audited", traceAttrs(attribute.Int64("count", 3)))
	s.End()
	_, s = tr.Start(ctx, "Other")
	s.End()
	td := otelio.SpansToTracesData(sr.Ended())
	steps, err := r.Steps([]otelio.TracesData{td})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 4 {
		t.Fatalf("%d steps: %+v", len(steps), steps)
	}
	d, w := steps[0], steps[1]
	if d.Action != "deposit" || d.Args["amount"] != int64(5) || d.Obs["balance"] != int64(5) || d.Actor != "bank" || d.Seq != 1 || d.Recorder != "i-1" || d.Outcome != "ok" {
		t.Fatalf("deposit: %+v", d)
	}
	if w.Action != "withdraw" || w.Outcome != "error" || w.Error != "insufficient funds" || w.Seq != 2 {
		t.Fatalf("withdraw: %+v", w)
	}
	if steps[2].Action != "audit" || steps[2].Args["n"] != int64(2) || steps[3].Args["n"] != int64(3) {
		t.Fatalf("events: %+v %+v", steps[2], steps[3])
	}
}

func traceAttrs(kv ...attribute.KeyValue) trace.EventOption { return trace.WithAttributes(kv...) }

// A domain clock and a thread per span: written "attr:x" or "attr:x|int",
// the clock is read (a second |int once dropped it silently), and each
// span its own thread.
func TestOrderAndThread(t *testing.T) {
	r, err := Parse([]byte(`
thread: span_id
rules:
  - span: "A"
    action: a
    order: { seq: attr:seq|int }
  - span: "B"
    action: b
    order: { seq: attr:seq }
`))
	if err != nil {
		t.Fatal(err)
	}
	sr := tracetest.NewSpanRecorder()
	tr := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)).Tracer("app")
	for _, n := range []string{"A", "B"} {
		_, s := tr.Start(context.Background(), n)
		s.SetAttributes(attribute.Int64("seq", 7))
		s.End()
	}
	steps, err := r.Steps([]otelio.TracesData{otelio.SpansToTracesData(sr.Ended())})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if s.Order["seq"] != 7 || s.Thread == "" {
			t.Errorf("%s: order %v, thread %q", s.Action, s.Order, s.Thread)
		}
	}
	if steps[0].Thread == steps[1].Thread {
		t.Error("two spans share a thread")
	}
}

// A rule's ref (or the rules' default) is each step's link to its
// artifact; without one a span's own quint.ref is kept, never a
// resource's, which would link every step.
func TestConvertRef(t *testing.T) {
	r, err := Parse([]byte(`
ref: attr:app.trace_line
rules:
  - span: "Shot"
    action: shot
    ref: attr:app.screenshot
  - span: "Line"
    action: line
`))
	if err != nil {
		t.Fatal(err)
	}
	bare, err := Parse([]byte(`
rules:
  - span: "*"
    action: step
`))
	if err != nil {
		t.Fatal(err)
	}
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr),
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "web"), attribute.String("quint.ref", "deployment"))))
	tr := tp.Tracer("app")
	_, s := tr.Start(context.Background(), "Shot")
	s.SetAttributes(attribute.String("app.screenshot", "shots/1.webp"), attribute.String("app.trace_line", "trace.jsonl:1"))
	s.End()
	_, s = tr.Start(context.Background(), "Line")
	s.SetAttributes(attribute.String("app.trace_line", "trace.jsonl:2"), attribute.String("quint.ref", "own"))
	s.End()
	_, s = tr.Start(context.Background(), "Plain")
	s.End()
	td := []otelio.TracesData{otelio.SpansToTracesData(sr.Ended())}
	steps, err := r.Steps(td)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || steps[0].Ref != "shots/1.webp" || steps[1].Ref != "trace.jsonl:2" {
		t.Fatalf("refs: %+v", steps)
	}
	steps, err = bare.Steps(td)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 || steps[0].Ref != "" || steps[1].Ref != "own" || steps[2].Ref != "" {
		t.Fatalf("a span's own ref: %+v", steps)
	}
}

// Unmatched lists the spans no rule matches by name, as Steps matches
// them: a dropped span is matched, an event rule matches a span only
// through an event of its name, and a where filter that fails leaves the
// span unmatched.
func TestUnmatched(t *testing.T) {
	r, err := Parse([]byte(rules + `
  - span: "Bank/Close"
    where: { bank.kind: savings }
    action: close
`))
	if err != nil {
		t.Fatal(err)
	}
	sr := tracetest.NewSpanRecorder()
	tr := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)).Tracer("app")
	ctx := context.Background()
	for _, name := range []string{"Bank/Deposit", "Bank/HealthCheck", "Other", "Other", "Bank/Audit"} {
		_, s := tr.Start(ctx, name)
		s.End()
	}
	_, s := tr.Start(ctx, "Bank/Audit")
	s.AddEvent("audited")
	s.End()
	_, s = tr.Start(ctx, "Bank/Close")
	s.SetAttributes(attribute.String("bank.kind", "checking"))
	s.End()
	got, err := r.Unmatched([]otelio.TracesData{otelio.SpansToTracesData(sr.Ended())})
	if err != nil {
		t.Fatal(err)
	}
	want := []Unmatched{{"Bank/Audit", 1}, {"Bank/Close", 1}, {"Other", 2}}
	if len(got) != len(want) {
		t.Fatalf("Unmatched = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Unmatched = %v, want %v", got, want)
		}
	}
}
