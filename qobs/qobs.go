// Package qobs is the runtime half of gotogether: it records model steps from a
// running Go program. It is what the //quint:action Orchestrion aspect calls,
// and it can be called directly.
//
// Recording is off until SetGlobal installs a Recorder, a context carries
// one (WithRecorder), or a Recorder is used directly, so linking qobs into a
// program changes nothing by itself.
package qobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/qrun"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"go.opentelemetry.io/otel/trace"
)

// Sink receives recorded steps. Record may be called concurrently.
type Sink interface {
	Record(ctx context.Context, s *qtrace.Step)
}

// Outcomer lets an error classify itself for the model, e.g. an S3 timeout
// that is known to have been applied reports "ambiguous". Without it a
// non-nil error is recorded as "error".
type Outcomer interface{ QuintOutcome() string }

// Recorder assigns each step a dense sequence number and hands it to a sink.
type Recorder struct {
	id      string
	seq     atomic.Uint64
	added   atomic.Uint64 // steps written by Add
	sink    Sink
	actor   string
	process string

	mu          sync.Mutex
	interrupted []qtrace.Step    // SetDiscard
	threads     map[string]int64 // a Thread's goroutine
	issues      []string
}

// Issues are what the recorder found wrong with how it was used, as it
// recorded: a Thread used from two goroutines (a thread asserts program
// order, which two goroutines do not have). An exploration fails a run
// with issues.
func (r *Recorder) Issues() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.issues...)
}

// checkThread notes the goroutine of a Thread's first step, and an issue
// when a later step comes from another.
func (r *Recorder) checkThread(thread, action string) {
	g := hooks.GoroutineID()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.threads == nil {
		r.threads = map[string]int64{}
	}
	first, ok := r.threads[thread]
	switch {
	case !ok:
		r.threads[thread] = g
	case first != g && len(r.issues) < 10:
		r.issues = append(r.issues, fmt.Sprintf("thread %s (qobs.Thread) recorded %q from goroutine %d after goroutine %d: "+
			"a thread asserts program order, which two goroutines do not have; give each its own (qobs.Thread in the goroutine)", thread, action, g, first))
		r.threads[thread] = g // one issue per change of goroutine
	}
}

// Interrupted returns the steps this recorder was asked to record by work
// that never completed (a qenv task unwinding after a crash or at the end
// of a run: SetDiscard), in the order asked. They are not in the run and
// not checked against the model: they say what a crash or the end of the
// run cut off, for reports.
func (r *Recorder) Interrupted() []qtrace.Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]qtrace.Step(nil), r.interrupted...)
}

// Option configures a Recorder.
type Option func(*Recorder)

// WithActor sets the default quint.actor of every step.
func WithActor(a string) Option { return func(r *Recorder) { r.actor = a } }

// WithProcess sets the default quint.process (incarnation) of every step.
// It defaults to the recorder id, which is fresh per Recorder.
func WithProcess(p string) Option { return func(r *Recorder) { r.process = p } }

// WithID sets the recorder id (default: random).
func WithID(id string) Option { return func(r *Recorder) { r.id = id } }

// NewRecorder returns a Recorder writing to sink.
func NewRecorder(sink Sink, opts ...Option) *Recorder {
	var b [6]byte
	_, _ = rand.Read(b[:]) //qenv:ok a recorder id, unique across processes; explorations compare runs up to renaming it
	r := &Recorder{id: hex.EncodeToString(b[:]), sink: sink}
	for _, o := range opts {
		o(r)
	}
	if r.process == "" {
		r.process = r.id
	}
	if r.actor == "" {
		r.actor = "default" // as qtrace.FromAttributes reads a step without one
	}
	return r
}

// Count is how many steps the recorder has recorded (Add's included): a
// run's progress, which its choices are aligned to (qenv.Choice.Step).
func (r *Recorder) Count() int { return int(r.seq.Load() + r.added.Load()) }

// ID is the recorder id (the scope of quint.seq).
func (r *Recorder) ID() string { return r.id }

// Actor is the recorder's default quint.actor (WithActor; "default").
func (r *Recorder) Actor() string { return r.actor }

// Add writes steps recorded elsewhere to the recorder's sink as they are,
// with their own recorder ids and sequence numbers: steps a simulation in
// another process sent back (gosim's, see examples/gosim).
func (r *Recorder) Add(steps ...qtrace.Step) {
	for i := range steps {
		r.sink.Record(context.Background(), &steps[i])
		r.added.Add(1)
	}
}

// Process-wide: InstallGlobal's.
var global atomic.Pointer[Recorder]

// SetGlobal installs the recorder used by the package-level Record (and so by
// woven //quint:action advice), for a program's lifetime: a main that
// records its steps to a sink. nil turns recording off. Tests install one
// for a run with qtest.Global() (InstallGlobal, which refuses a second and
// restores what was there), and a Hegel case with qhegel's Case.Install.
func SetGlobal(r *Recorder) { global.Store(r) }

// Global returns the installed recorder, or nil (unrelated to the option
// qtest.Global, which installs one for a test's run).
func Global() *Recorder { return global.Load() }

// installed is the recorder InstallGlobal put in place, while it is.
// Process-wide.
var installed atomic.Pointer[Recorder]

// InstallGlobal installs r as the global recorder for a test's run
// (qtest.Global, in a System's or qhegel's Opts): the steps of goroutines no
// run can follow (a server's, a cluster's) go to it. A second install
// while one is in place fails: the two runs would take each other's
// steps, so tests that install it must not run in parallel. restore puts
// back the recorder that was there before, unless something else has
// replaced r since (it is not undone by identity, then: a test's
// restore cannot remove another's recorder).
func InstallGlobal(r *Recorder) (restore func(), err error) {
	if r == nil {
		return nil, errors.New("qobs: InstallGlobal(nil)")
	}
	if !installed.CompareAndSwap(nil, r) {
		return nil, errors.New("qobs: another run's global recorder is installed: runs that install one (qtest.Global, " +
			"qhegel's Config.Global) must not run in parallel; drop t.Parallel, or record through the run's context")
	}
	prev := global.Swap(r)
	var once sync.Once
	return func() {
		once.Do(func() {
			global.CompareAndSwap(r, prev)
			installed.CompareAndSwap(r, nil)
		})
	}, nil
}

var recorderKey = qrun.NewKey[*Recorder]("recorder")

// WithRecorder returns a context whose steps Record sends to r rather than
// to the global recorder. Runs in parallel (seeds of an exploration,
// simulations of a trigger search) each record into their own: a qenv
// simulation passes its context to every task (qenv.WithContext).
func WithRecorder(ctx context.Context, r *Recorder) context.Context {
	return qrun.With(ctx, recorderKey, r)
}

// From returns the recorder Record uses for ctx: the one WithRecorder set;
// else that of the run the goroutine belongs to (qrun.Get: a step recorded
// with context.Background() inside a simulation still lands in its own
// run, even with runs in parallel); else the global one (nil if none).
func From(ctx context.Context) *Recorder {
	if r, ok := qrun.Get(ctx, recorderKey); ok && r != nil {
		return r
	}
	return global.Load()
}

// Record records one step through the context's recorder (WithRecorder),
// its goroutine's run's (qrun) or the global one; a no-op when
// there is none, unless a run is Strict. This is the function
// //quint:action advice calls.
//
//	qobs.Record(ctx, "deposit", err, "account", acct, "amount", n, "obs.balance", bal)
//
// action is the model's action; a key is one of its parameters
// ("account", "amount"), or "obs.<name>", what the code observed after
// the step (the binding's expect compares it with the model's state); a
// non-nil err makes the step's outcome "error" (an "outcome" key names
// another). Recorder.Record lists the other keys (actor, process, thread,
// outcome, order.<d>, invoked, when).
func Record(ctx context.Context, action string, err error, kv ...any) {
	if r := From(ctx); r != nil {
		r.Record(ctx, action, err, kv...)
		return
	}
	if hooks.Discarding() {
		return // interrupted work with nowhere to report it
	}
	if hooks.StrictOn() {
		hooks.Escape(fmt.Sprintf("qobs: step %q has no recorder: its context carries none, its goroutine belongs to no "+
			"run, and no global recorder is installed. %s", action, escapeAdvice()))
	}
}

// escapeAdvice is what to do about a step that escaped a strict run,
// for the goroutine it came from: an HTTP handler's (net/http serves
// each connection on a goroutine of its own) is told to wrap the
// handler, one in a process running a simulation to start it with
// qenv.Go, any other to be given the run's context. It reads the
// goroutine's stack, on this path only.
func escapeAdvice() string {
	buf := make([]byte, 64<<10)
	stack := string(buf[:runtime.Stack(buf, false)])
	switch {
	case strings.Contains(stack, "net/http.serverHandler.ServeHTTP(") || strings.Contains(stack, "net/http.(*conn).serve("):
		return "It is an HTTP handler's (net/http serves each connection on a goroutine of its own): wrap the handler " +
			"so that its requests carry the run's recorder (qobshttp.Handler(h, rec), rec the run's: qtest.Record's " +
			"scenario's argument), or pass qtest.Global(), which keeps tests from running in parallel. A goroutine the " +
			"handler starts must record through the request's context (r.Context())."
	case hooks.Simulating():
		return "A goroutine started with `go` is outside the simulation: start it with qenv.Go, or record through " +
			"a context from the run (qenv.WithContext)."
	}
	return "A goroutine started with `go` is outside the run (qrun.Start covers only the goroutine that called it): " +
		"record through a context from the run (qobs.WithRecorder(ctx, rec), rec qtest.Record's scenario's argument), " +
		"or pass qtest.Global() for goroutines the code starts, which keeps tests from running in parallel."
}

type threadKey struct{}

// Process-wide: thread ids unique across runs.
var threadSeq atomic.Uint64

// Thread returns a context whose steps are in program order with each other
// (quint.thread). Use it where one goroutine performs a sequence of steps and
// no OTel span marks that sequence. Without it, the span in ctx is the thread.
func Thread(ctx context.Context) context.Context {
	return context.WithValue(ctx, threadKey{}, fmt.Sprintf("t%d", threadSeq.Add(1)))
}

// Record records one step. kv alternates keys and values:
//
//	"actor", "process", "thread", "outcome"  override the step's identity
//	"when"        "ok" | "error": record only if err is nil / non-nil
//	"obs.<name>"  an observed projection of the state after the step
//	"order.<d>"   a domain logical clock (see qtrace.Reconstruct)
//	"invoked"     a time.Time: when the operation began (qtrace.Step.Invoked),
//	              for a step recorded where it returned, not where it took
//	              effect (a client's); qtest.Linearize orders by it
//	anything else an argument of the action (quint.arg.<key>)
//
// Values are normalised to string, int64, bool or float64. A Ref, in a
// key's place, links the step to its artifact (qtrace.Step.Ref).
func (r *Recorder) Record(ctx context.Context, action string, err error, kv ...any) {
	r.record(ctx, action, err, expand(kv), 0)
}

// A StepRef is a step's link to its artifact (Ref).
type StepRef string

// Ref links the step it is recorded with to its artifact (quint.ref,
// qtrace.Step.Ref): an external driver's trace line, a screenshot. It
// stands in a key's place, alone, among Record's (and Invoke's and Done's)
// keys:
//
//	qobs.Record(ctx, "submit", err, qobs.Ref("bombadil/trace.jsonl:42 screenshot=screenshots/42.webp"), "job", id)
//
// A typed value, not a "ref" key: a model action may have a parameter
// named ref. A failing step's report and validate -history show it; no
// verdict depends on it.
func Ref(s string) StepRef { return StepRef(s) }

// refKey is the key a Ref is expanded to: no string, so no argument's.
type refKey struct{}

// expand turns each Ref in a key's place into a key and its value, so
// the keys are pairs again for everything after.
func expand(kv []any) []any {
	n := -1
	for i := 0; i < len(kv); i += 2 {
		if _, ok := kv[i].(StepRef); ok {
			n = i
			break
		}
	}
	if n < 0 {
		return kv // the common case: no Ref, no copy
	}
	out := append([]any(nil), kv[:n]...)
	for i := n; i < len(kv); {
		if ref, ok := kv[i].(StepRef); ok {
			out = append(out, refKey{}, string(ref))
			i++
			continue
		}
		if i+1 < len(kv) {
			out = append(out, kv[i], kv[i+1])
		}
		i += 2
	}
	return out
}

// record records a step, the completion of invocation call if not 0.
func (r *Recorder) record(ctx context.Context, action string, err error, kv []any, call uint64) {
	if r == nil {
		return
	}
	s, ok := r.step(ctx, action, err, kv)
	if !ok {
		return
	}
	s.Call = call
	if hooks.Discarding() {
		s.Time = hooks.Now()
		r.mu.Lock()
		r.interrupted = append(r.interrupted, *s)
		r.mu.Unlock()
		return
	}
	// seq is taken here, after the effect the step describes has happened.
	// Two concurrent steps may be numbered in either order; the
	// reconstructor only relies on seq within a thread (see qtrace).
	s.Seq = r.seq.Add(1)
	s.Time = hooks.Now()
	r.sink.Record(ctx, s)
}

// A Call is an operation recorded when it is invoked (Invoke): its
// invocation record is written at once, so a trace whose caller died, or
// whose call was never answered, says "invoked, never completed"
// (qtrace.Trace.Pending, a report's pending calls) instead of nothing.
type Call struct {
	r      *Recorder
	ctx    context.Context
	action string
	kv     []any
	seq    uint64
	done   atomic.Bool
}

// Invoke records the invocation of an operation through the context's
// recorder, found as Record finds it, and returns the call, whose Done
// records the completed step. kv are the step's keys known at invocation
// (its arguments, actor, process, thread); Done adds the rest.
//
//	c := qobs.Invoke(ctx, "transfer", "src", a, "dest", b, "amount", n)
//	err := client.Transfer(ctx, a, b, n)
//	c.Done(err)
//
// With no recorder it returns nil, whose Done does nothing.
func Invoke(ctx context.Context, action string, kv ...any) *Call {
	if r := From(ctx); r != nil {
		return r.Invoke(ctx, action, kv...)
	}
	Record(ctx, action, nil, kv...) // the same no-op, or strict failure
	return nil
}

// Invoke records the invocation of an operation and returns the call (see
// the package's Invoke).
func (r *Recorder) Invoke(ctx context.Context, action string, kv ...any) *Call {
	if r == nil {
		return nil
	}
	c := &Call{r: r, ctx: ctx, action: action, kv: append([]any(nil), expand(kv)...)}
	if hooks.Discarding() {
		return c // interrupted work: its Done, if any, goes to Interrupted
	}
	var keys []any // "when" is the completion's: the invocation is recorded whatever
	for i := 0; i+1 < len(c.kv); i += 2 {
		if c.kv[i] != "when" {
			keys = append(keys, c.kv[i], c.kv[i+1])
		}
	}
	s, _ := r.step(ctx, action, nil, keys)
	s.Invocation, s.Outcome = true, ""
	c.seq = r.seq.Add(1)
	s.Seq, s.Time = c.seq, hooks.Now()
	r.sink.Record(ctx, s)
	return c
}

// Done records the call's completed step: the invocation's keys, then
// kv (Record's keys: obs.*, outcome, ...), with err its outcome. Its
// real-time interval is the invocation record's time to its own
// (qtrace.Reconstruct pairs them; an "invoked" key is overridden), and
// on one recorder the records' order is the calls' exact real-time order. It always records: "when" keys, which would
// leave the call pending, are dropped. A second Done does nothing.
func (c *Call) Done(err error, kv ...any) {
	if c == nil || !c.done.CompareAndSwap(false, true) {
		return
	}
	var all []any
	for _, l := range [][]any{c.kv, expand(kv)} {
		for i := 0; i+1 < len(l); i += 2 {
			if l[i] != "when" {
				all = append(all, l[i], l[i+1])
			}
		}
	}
	c.r.record(c.ctx, c.action, err, all, c.seq)
}

// Settled records, through the context's recorder (found as Record finds
// it), that the system has settled: nothing is pending, so the binding's
// eventual properties apply to the run if nothing is recorded after it.
// qenv.Sim.Settle records it when it finds the system quiet; a real
// deployment records it where it knows (every client done, queues
// drained). A no-op with no recorder.
func Settled(ctx context.Context) {
	if r := From(ctx); r != nil {
		r.Settled(ctx)
	}
}

// Settled records that the system has settled (the package's Settled).
func (r *Recorder) Settled(ctx context.Context) {
	if r == nil || hooks.Discarding() {
		return
	}
	s := &qtrace.Step{Action: "settled", Recorder: r.id, Actor: r.actor, Process: r.process, Settled: true}
	s.Seq, s.Time = r.seq.Add(1), hooks.Now()
	r.sink.Record(ctx, s)
}

// CompleteThrough records, through the context's recorder (found as
// Record finds it), that its steps recorded at or before t are all
// delivered (CompleteThrough of the Recorder). A no-op with no recorder.
func CompleteThrough(ctx context.Context, t time.Time) {
	if r := From(ctx); r != nil {
		r.CompleteThrough(ctx, t)
	}
}

// CompleteThrough records a watermark (qtrace.KeyCompleteThrough): every
// step r recorded at or before t has reached its sink's destination, so
// a validation that reads the destination may judge an absent step
// whose window ends by t as absent, not as not collected yet. Write it
// where delivery is known: after the sink's flush, or a collector's
// acknowledgement of everything up to t. A watermark is a record, not a
// step, and takes no seq. A log that states none is taken whole.
func (r *Recorder) CompleteThrough(ctx context.Context, t time.Time) {
	if r == nil || hooks.Discarding() || t.IsZero() {
		return
	}
	s := &qtrace.Step{Action: "complete", Recorder: r.id, Actor: r.actor, Process: r.process, CompleteThrough: t, Time: hooks.Now()}
	r.sink.Record(ctx, s)
}

// step builds a step from Record's arguments; false if "when" excludes it.
func (r *Recorder) step(ctx context.Context, action string, err error, kv []any) (*qtrace.Step, bool) {
	s := qtrace.Step{Action: action, Recorder: r.id, Actor: r.actor, Process: r.process, Outcome: outcome(err)}
	if err != nil {
		s.Error = err.Error()
	}
	if t, ok := ctx.Value(threadKey{}).(string); ok {
		s.Thread = t
		r.checkThread(t, action)
	} else if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		s.Thread = sc.SpanID().String()
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if _, ok := kv[i].(refKey); ok {
			s.Ref = kv[i+1].(string)
			continue
		}
		k, _ := kv[i].(string)
		if t, ok := kv[i+1].(time.Time); ok && k == "invoked" {
			s.Invoked = t
			continue
		}
		v := normalize(kv[i+1])
		switch {
		case k == "when":
			if (v == "ok" && err != nil) || (v == "error" && err == nil) {
				return nil, false
			}
		case k == "actor":
			s.Actor = fmt.Sprint(v)
		case k == "process":
			s.Process = fmt.Sprint(v)
		case k == "thread":
			s.Thread = fmt.Sprint(v)
		case k == "outcome":
			s.Outcome = fmt.Sprint(v)
		case len(k) > 4 && k[:4] == "obs.":
			if s.Obs == nil {
				s.Obs = map[string]any{}
			}
			s.Obs[k[4:]] = v
		case len(k) > 6 && k[:6] == "order.":
			if n, ok := v.(int64); ok {
				if s.Order == nil {
					s.Order = map[string]int64{}
				}
				s.Order[k[6:]] = n
			}
		default:
			if s.Args == nil {
				s.Args = map[string]any{}
			}
			s.Args[k] = v
		}
	}
	return &s, true
}

func outcome(err error) string {
	if err == nil {
		return "ok"
	}
	var o Outcomer
	if errors.As(err, &o) {
		return o.QuintOutcome()
	}
	return "error"
}

// normalize maps a Go value to an attribute-friendly one.
func normalize(v any) any {
	switch x := v.(type) {
	case nil:
		return ""
	case string, bool, int64, float64:
		return x
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case uint:
		return int64(x)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case uint64:
		return int64(x)
	case float32:
		return float64(x)
	case fmt.Stringer:
		return x.String()
	case error:
		return x.Error()
	}
	// Collections (slices, arrays, maps) travel as JSON text: an attribute
	// or step-log field holds a scalar. A binding's toSet / toList turn it
	// back into a Quint value.
	switch reflect.ValueOf(v).Kind() {
	case reflect.Slice, reflect.Array, reflect.Map:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
	}
	return fmt.Sprint(v)
}

// MemorySink keeps steps in memory: for tests, and for validating a run
// in-process without any OTel pipeline.
type MemorySink struct {
	mu    sync.Mutex
	steps []qtrace.Step
}

func (m *MemorySink) Record(_ context.Context, s *qtrace.Step) {
	m.mu.Lock()
	m.steps = append(m.steps, *s)
	m.mu.Unlock()
}

// Steps returns a copy of what was recorded.
func (m *MemorySink) Steps() []qtrace.Step {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]qtrace.Step(nil), m.steps...)
}

// Reset forgets everything recorded.
func (m *MemorySink) Reset() { m.mu.Lock(); m.steps = nil; m.mu.Unlock() }

// Tee records to several sinks.
type Tee []Sink

func (t Tee) Record(ctx context.Context, s *qtrace.Step) {
	for _, x := range t {
		x.Record(ctx, s)
	}
}
