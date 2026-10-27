// Package hooks is the core's plumbing between its packages, kept out of
// their public API: the recording rules a simulated run turns on (qobs
// consults them; qtest, qload and qenv set them), how a goroutine's run is
// found (qenv resolves it; qrun asks), and where a run wants a task's
// panic (qtest asks; qenv writes it). It imports nothing of the module,
// so every package may import it.
package hooks

import (
	"bytes"
	"context"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Process-wide: the hooks qenv installs, and the strict runs in progress.
var (
	discard atomic.Pointer[func() bool]
	strict  atomic.Int64
)

// SetDiscard installs a check, made for every step, of whether the
// calling goroutine is unwinding work that never completed: a qenv task
// killed by a crash, unwinding when its simulation closes. A step recorded
// there would claim a completion that did not happen. qenv installs it.
func SetDiscard(f func() bool) { discard.Store(&f) }

// Discarding reports whether the calling goroutine's steps are discarded
// (SetDiscard's check).
func Discarding() bool {
	f := discard.Load()
	return f != nil && (*f)()
}

// Strict turns on, until release is called, the rule that a step must
// have somewhere to go: one with no recorder is logged as escaped
// (Escape) instead of dropped silently. Simulated runs (qtest's
// explorations, qload.Experiment) are strict, and fail when a step
// escaped. It is a log, not a panic: the step comes from a goroutine no
// run owns, whose panic nothing could recover.
func Strict() (release func()) {
	strict.Add(1)
	return sync.OnceFunc(func() { strict.Add(-1) })
}

// StrictOn reports whether a strict run is on.
func StrictOn() bool { return strict.Load() > 0 }

// FromSimulation reports whether the calling goroutine is a simulation's
// (a task, or the goroutine that created one) or was started by one with
// `go`: where an escaped step's message advises qenv.Go. It asks the
// goroutine's origin, not whether any simulation is open in the process:
// a test recording a real server in parallel with an exploration is told
// what fits it.
func FromSimulation() bool {
	f := creatorRun.Load()
	if f == nil {
		return false
	}
	if _, ok := (*f)(GoroutineID()); ok {
		return true
	}
	if id, ok := creatorID(); ok {
		_, ok := (*f)(id)
		return ok
	}
	return false
}

// escaped logs the steps strict runs could not place, process-wide: their
// goroutine names no run, so no run can be told.
var escaped struct {
	sync.Mutex
	n    int
	msgs []string // the first maxEscaped
}

const maxEscaped = 1000

// Escape logs a step that escaped a strict run, with its message: to the
// run of the goroutine that started the step's goroutine, if that run
// keeps an escape log (WithEscapes), else process-wide (Escaped), where
// every strict run running sees it.
func Escape(msg string) {
	if f := creatorRun.Load(); f != nil {
		if id, ok := creatorID(); ok {
			if ctx, ok := (*f)(id); ok {
				if e, ok := ctx.Value(escapesKey{}).(*Escapes); ok && e.add(msg) {
					return
				}
			}
		}
	}
	escaped.Lock()
	defer escaped.Unlock()
	escaped.n++
	if len(escaped.msgs) < maxEscaped {
		escaped.msgs = append(escaped.msgs, msg)
	}
}

// Escaped returns the number of steps that escaped strict runs so far,
// and the messages of those after mark (an earlier count): a strict run
// takes the count when it starts and reports what escaped while it ran.
// Past the first 1000 the messages are not kept, and since holds one that
// says so.
func Escaped(mark int) (n int, since []string) {
	escaped.Lock()
	defer escaped.Unlock()
	if mark < len(escaped.msgs) {
		since = append(since, escaped.msgs[mark:]...)
	}
	if escaped.n > mark && len(since) == 0 {
		since = []string{strconv.Itoa(escaped.n-mark) + " steps had no recorder (their messages are past the first " + strconv.Itoa(maxEscaped) +
			" kept): a goroutine started with `go` is outside the run; start it with qenv.Go in a simulation, " +
			"wrap an HTTP handler with qobshttp.Handler, or record through a context from the run (qobs.WithRecorder)"}
	}
	return escaped.n, since
}

// Escapes is one run's log of the steps that escaped it: recorded from a
// goroutine one of its tasks started with `go`, which no run owns.
type Escapes struct {
	mu     sync.Mutex
	n      int
	msgs   []string // the first maxEscaped
	closed bool
}

// add logs msg, or reports false once the log is closed.
func (e *Escapes) add(msg string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return false
	}
	e.n++
	if len(e.msgs) < maxEscaped {
		e.msgs = append(e.msgs, msg)
	}
	return true
}

// Close ends the log when its run is over: a step that escapes it later
// (from a goroutine of a simulation never closed) is logged process-wide,
// where the strict runs still running, and the exploration's own check,
// see it.
func (e *Escapes) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
}

// Steps returns how many steps escaped the run, and the first one's
// message.
func (e *Escapes) Steps() (n int, first string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.msgs) > 0 {
		first = e.msgs[0]
	}
	return e.n, first
}

type escapesKey struct{}

// WithEscapes returns ctx logging the steps that escape the run it is the
// context of into e (Escape): a run that keeps its own log is not told of
// other runs' escapes, nor they of its own.
func WithEscapes(ctx context.Context, e *Escapes) context.Context {
	return context.WithValue(ctx, escapesKey{}, e)
}

// Process-wide: installed by qenv.
var creatorRun atomic.Pointer[func(int64) (context.Context, bool)]

// SetCreatorRun installs how a goroutine's run context is found by its
// id, for the goroutine that started an escaped step's: qenv returns the
// context of a simulation's task (running or ended, until the simulation
// closes), or of the simulation's creator.
func SetCreatorRun(f func(id int64) (context.Context, bool)) { creatorRun.Store(&f) }

// creatorID is the id of the goroutine that started the calling one, from
// the last line of its stack ("created by f in goroutine 7"): false for
// the main goroutine, or a stack too deep to read whole.
func creatorID() (int64, bool) {
	buf := make([]byte, 64<<10)
	n := runtime.Stack(buf, false)
	if n == len(buf) {
		return 0, false // truncated: its last line is gone
	}
	b := buf[:n]
	const in = " in goroutine "
	i := bytes.LastIndex(b, []byte("\ncreated by "))
	if i < 0 {
		return 0, false
	}
	line := b[i+1:]
	if j := bytes.IndexByte(line, '\n'); j >= 0 {
		line = line[:j]
	}
	k := bytes.LastIndex(line, []byte(in))
	if k < 0 {
		return 0, false
	}
	id, err := strconv.ParseInt(string(line[k+len(in):]), 10, 64)
	return id, err == nil
}

// KnownStatic reports whether a name users give (a Rates key, a sweep's
// FaultKind) covers a fault kind, option or nemesis declared outside any
// run (a woven fault point's, at init), or a prefix of one: qenv installs
// it, qtest's workflows ask it and the runs' FaultNames. Process-wide.
var KnownStatic = func(string) bool { return true }

// FaultNames are what one run declared or chose: fault kinds, their
// options (kind.option) and nemeses (nemesis.<name>), as reports print
// them. A name another run's double declared is not one of them, so a
// Rates key that covers nothing of this run's fails its exploration
// whatever ran in parallel.
type FaultNames struct {
	mu    sync.Mutex
	names map[string]bool
}

// Add notes names.
func (f *FaultNames) Add(names ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.names == nil {
		f.names = map[string]bool{}
	}
	for _, n := range names {
		f.names[n] = true
	}
}

// Covers reports whether name is one of the names, or a prefix of one at
// a dot ("queue" of "queue.payments.ack").
func (f *FaultNames) Covers(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.names[name] {
		return true
	}
	for n := range f.names {
		if strings.HasPrefix(n, name+".") {
			return true
		}
	}
	return false
}

type faultNamesKey struct{}

// WithFaultNames returns ctx carrying a run's FaultNames, where the
// run's simulations and doubles add to them.
func WithFaultNames(ctx context.Context, f *FaultNames) context.Context {
	return context.WithValue(ctx, faultNamesKey{}, f)
}

// FaultNamesOf is ctx's run's FaultNames, or nil.
func FaultNamesOf(ctx context.Context) *FaultNames {
	if ctx == nil {
		return nil
	}
	f, _ := ctx.Value(faultNamesKey{}).(*FaultNames)
	return f
}

// Process-wide: installed by qenv.
var resolver atomic.Pointer[func() (context.Context, bool)]

// SetResolver installs how the calling goroutine's run context is found:
// qenv returns the context of the goroutine's simulation task, or of the
// simulation it created.
func SetResolver(f func() (context.Context, bool)) { resolver.Store(&f) }

// Resolve is the calling goroutine's run context by the resolver, if one
// is installed and the goroutine is a run's.
func Resolve() (context.Context, bool) {
	if f := resolver.Load(); f != nil {
		return (*f)()
	}
	return nil, false
}

// GoroutineID is the calling goroutine's id, from the runtime's stack
// header ("goroutine 123 [running]:"): Go has no goroutine-local storage.
func GoroutineID() int64 {
	var buf [32]byte
	b := buf[:runtime.Stack(buf[:], false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	if i := bytes.IndexByte(b, ' '); i > 0 {
		b = b[:i]
	}
	id, _ := strconv.ParseInt(string(b), 10, 64)
	return id
}

// A TaskPanic is a simulation task's panic: its value and the task's
// stack, which a recover around Sim.Run cannot see.
type TaskPanic struct {
	Value any
	Stack string
}

// Is reports whether p is the panic of value v: Run re-raised it, rather
// than the caller panicking anew after recovering it. Values of types
// that do not compare are never the same.
func (p *TaskPanic) Is(v any) bool {
	if p == nil || p.Value == nil || v == nil {
		return false
	}
	if t := reflect.TypeOf(v); !t.Comparable() || t != reflect.TypeOf(p.Value) {
		return false
	}
	return p.Value == v
}

type panicKey struct{}

// WithPanicStack returns ctx asking simulations created with it (their
// chooser's context) to write a panicking task's value and stack into
// *dst before Run re-raises the panic.
func WithPanicStack(ctx context.Context, dst *TaskPanic) context.Context {
	return context.WithValue(ctx, panicKey{}, dst)
}

// PanicStackOf is where ctx wants a task's panic (WithPanicStack).
func PanicStackOf(ctx context.Context) (*TaskPanic, bool) {
	dst, ok := ctx.Value(panicKey{}).(*TaskPanic)
	return dst, ok
}

// Process-wide: installed by qenv.
var clock atomic.Pointer[func() (time.Time, bool)]

// SetClock installs qenv's clock: the virtual time of the simulation the
// calling goroutine belongs to (a task's, or its creator's), so a step
// recorded in a simulation carries the simulation's time, not the wall
// clock's, and a client history recorded there has intervals to
// linearize.
func SetClock(f func() (time.Time, bool)) { clock.Store(&f) }

// Now is the time a step recorded now carries: the simulation's, else the
// wall clock's.
func Now() time.Time {
	if f := clock.Load(); f != nil {
		if t, ok := (*f)(); ok {
			return t
		}
	}
	return time.Now()
}

// CompleteSearchOnly makes qtrace's linearization skip its guided search,
// so a test of another package can check the complete search alone (it
// finds what the guided one misses): process-wide, for tests.
var CompleteSearchOnly atomic.Bool

// TamperEval makes validate's Go evaluator wrong, for tests that show
// quint's confirmation catches it (1: it rejects every candidate at its
// first step, 2: it accepts every one, 3: it rejects every candidate of
// an odd number of steps; and in its search over hidden actions' or a
// resume's states, 4: no state takes the first step, 5: every expect
// holds).
//
// Process-wide: a test's.
var TamperEval atomic.Int32
