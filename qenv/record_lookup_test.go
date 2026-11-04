package qenv_test

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"go.opentelemetry.io/otel/trace"
)

type recordLookupStringer func() string

func (f recordLookupStringer) String() string { return f() }

type recordLookupSink struct {
	memory   qobs.MemorySink
	contexts []context.Context
}

func (s *recordLookupSink) Record(ctx context.Context, step *qtrace.Step) {
	s.contexts = append(s.contexts, ctx)
	s.memory.Record(ctx, step)
}

// Formatting an attribute can enter another simulation. Recorder selection
// precedes formatting, but the final stamp belongs to the current owner after
// it. A cached run context or site would incorrectly stamp the outer clock.
func TestRecordLookupRefreshesOwnerAfterNormalization(t *testing.T) {
	sink := &recordLookupSink{}
	rec := qobs.NewRecorder(sink)
	outer := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(context.Background(), rec)))
	defer outer.Close()
	caller := context.Background()
	var innerErr error
	outer.Spawn("outer", func(ctx context.Context) {
		qenv.Sleep(ctx, 2*time.Second)
		var inner *qenv.Sim
		defer func() {
			if inner != nil {
				inner.Close()
			}
		}()
		qobs.Record(caller, "nested", nil, "value", recordLookupStringer(func() string {
			inner = qenv.New(qenv.WithContext(qenv.Nominal{}, context.Background()))
			inner.Spawn("advance", func(ctx context.Context) { qenv.Sleep(ctx, 5*time.Second) })
			innerErr = inner.Run(6 * time.Second)
			return "formatted"
		}))
	})
	if err := outer.Run(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	if innerErr != nil {
		t.Fatal(innerErr)
	}
	steps := sink.memory.Steps()
	if len(steps) != 1 || steps[0].Recorder != rec.ID() || steps[0].Args["value"] != "formatted" || !steps[0].Time.Equal(qenv.Epoch.Add(5*time.Second)) {
		t.Fatalf("step after nested ownership change: %+v", steps)
	}
	if len(sink.contexts) != 1 || sink.contexts[0] != caller {
		t.Fatalf("sink context was replaced: %v", sink.contexts)
	}
}

// An invocation retains its recorder and caller context, but completion can run
// on another task. Its site must be resolved on that completing goroutine.
func TestRecordLookupDoneUsesCompletingTaskSite(t *testing.T) {
	aSink, bSink := &recordLookupSink{}, &recordLookupSink{}
	aRec, bRec := qobs.NewRecorder(aSink), qobs.NewRecorder(bSink)
	a := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(context.Background(), aRec)))
	defer a.Close()
	b := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(context.Background(), bRec)))
	defer b.Close()
	caller := context.Background()
	var call *qobs.Call
	a.Spawn("invoke", func(ctx context.Context) {
		qenv.Sleep(ctx, 2*time.Second)
		call = qobs.Invoke(caller, "request", "id", 7)
	})
	if err := a.Run(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	if call == nil {
		t.Fatal("invocation found no recorder")
	}
	b.Spawn("complete", func(ctx context.Context) {
		qenv.Sleep(ctx, 7*time.Second)
		call.Done(nil, "obs.done", true)
	})
	if err := b.Run(8 * time.Second); err != nil {
		t.Fatal(err)
	}
	steps := aSink.memory.Steps()
	if len(steps) != 2 || !steps[0].Invocation || steps[1].Call != steps[0].Seq || steps[1].Recorder != aRec.ID() || !steps[0].Time.Equal(qenv.Epoch.Add(2*time.Second)) || !steps[1].Time.Equal(qenv.Epoch.Add(7*time.Second)) {
		t.Fatalf("invocation/completion sites: %+v", steps)
	}
	if len(bSink.memory.Steps()) != 0 {
		t.Fatalf("completion moved to the completing task's recorder: %+v", bSink.memory.Steps())
	}
	for _, ctx := range aSink.contexts {
		if ctx != caller {
			t.Fatal("call's original context was replaced")
		}
	}
}

// Recorder selection and site selection have independent context precedence.
// A foreign task can supply the clock while the current task supplies a missing
// recorder. An explicit nil recorder shadows that fallback and selects global.
func TestRecordLookupPreservesContextPrecedence(t *testing.T) {
	runSink, explicitSink, globalSink := &recordLookupSink{}, &recordLookupSink{}, &recordLookupSink{}
	runRec, explicitRec, globalRec := qobs.NewRecorder(runSink), qobs.NewRecorder(explicitSink), qobs.NewRecorder(globalSink)
	restore, err := qobs.InstallGlobal(globalRec)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	a := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(context.Background(), runRec)))
	defer a.Close()
	b := qenv.New(qenv.WithContext(qenv.Nominal{}, context.Background()))
	defer b.Close()
	var foreign context.Context
	b.Spawn("foreign", func(ctx context.Context) {
		foreign = ctx
		qenv.Sleep(ctx, 3*time.Second)
	})
	if err := b.Run(4 * time.Second); err != nil {
		t.Fatal(err)
	}
	span := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	caller := trace.ContextWithSpanContext(context.Background(), span)
	nilRecorder := qobs.WithRecorder(foreign, nil)
	explicit := qobs.WithRecorder(trace.ContextWithSpanContext(foreign, span), explicitRec)
	a.Spawn("record", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Second)
		qobs.Record(caller, "caller", nil)
		qobs.Record(foreign, "foreign", nil)
		qobs.Record(nilRecorder, "global", nil)
		qobs.Record(explicit, "explicit", nil)
	})
	if err := a.Run(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	run := runSink.memory.Steps()
	if len(run) != 2 || run[0].Action != "caller" || run[0].Thread != span.SpanID().String() || !run[0].Time.Equal(qenv.Epoch.Add(time.Second)) || run[1].Action != "foreign" || !run[1].Time.Equal(qenv.Epoch.Add(3*time.Second)) {
		t.Fatalf("run recorder and independent sites: %+v", run)
	}
	if len(runSink.contexts) != 2 || runSink.contexts[0] != caller || runSink.contexts[1] != foreign {
		t.Fatal("fallback replaced the caller context or foreign-task context")
	}
	for _, tc := range []struct {
		name string
		sink *recordLookupSink
		ctx  context.Context
	}{
		{"global", globalSink, nilRecorder},
		{"explicit", explicitSink, explicit},
	} {
		steps := tc.sink.memory.Steps()
		if len(steps) != 1 || steps[0].Action != tc.name || !steps[0].Time.Equal(qenv.Epoch.Add(3*time.Second)) || len(tc.sink.contexts) != 1 || tc.sink.contexts[0] != tc.ctx {
			t.Fatalf("%s recorder override: %+v", tc.name, steps)
		}
	}
	if got := explicitSink.memory.Steps()[0].Thread; got != span.SpanID().String() {
		t.Fatalf("explicit recorder lost caller span: %q", got)
	}
}
