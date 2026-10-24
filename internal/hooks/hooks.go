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
	"sync"
	"sync/atomic"
)

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
	var once sync.Once
	return func() { once.Do(func() { strict.Add(-1) }) }
}

// StrictOn reports whether a strict run is on.
func StrictOn() bool { return strict.Load() > 0 }

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
			" kept): a goroutine started with `go` is outside the simulation; start it with qenv.Go"}
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

// KnownFault reports whether a name users give (a Rates key, a sweep's
// FaultKind) covers a fault kind, option or nemesis this process declared
// or chose, or a prefix of one: qenv installs it, qtest's workflows ask.
var KnownFault = func(string) bool { return true }

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

// Settled is where a run learns that its simulation settled (qenv's
// Settle found the system quiet): qtest judges a binding's eventual
// properties only on such a run.
type Settled struct{ Yes bool }

type settledKey struct{}

// WithSettled returns ctx with s for a simulation made with it (its
// chooser's context) to mark.
func WithSettled(ctx context.Context, s *Settled) context.Context {
	return context.WithValue(ctx, settledKey{}, s)
}

// SettledOf is ctx's Settled, if it has one.
func SettledOf(ctx context.Context) (*Settled, bool) {
	s, ok := ctx.Value(settledKey{}).(*Settled)
	return s, ok
}
