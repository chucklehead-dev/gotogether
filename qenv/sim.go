package qenv

import (
	"cmp"
	"container/heap"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/internal/runkeys"
	"github.com/chucklehead-dev/gotogether/qenv/internal/choice"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qrun"
)

// Sim runs tasks one at a time. A task runs until it reaches a seam (Yield,
// Wait, Sleep, or an operation of a simulated component); there the
// scheduler picks, with a schedule choice, which runnable task goes next.
// Code between seams is atomic, so a run is a sequence of choices, and the
// same choices give the same run.
//
// Time is virtual: it stands still while any task can run and jumps to the
// next deadline when none can.
//
// Code under test spawns goroutines with Go (not the go statement) and
// reads time with Now and Sleep, so they stay inside the simulation. Real
// goroutines, real time and map iteration order are outside it: a run that
// depends on them is not reproducible (Replay detects that).
type Sim struct {
	// MaxSteps bounds a run's scheduling steps (default 100000): a
	// livelock is reported, not looped.
	MaxSteps int
	// Log, if set, receives one line per fault and crash.
	Log func(string)
	// Profile, if set, accumulates the virtual time tasks spend waiting at
	// each seam, by stack (Profile.WriteTo: pprof's format).
	Profile *TimeProfile
	// NemesisEvery, if set, makes a nemesis (a crash) a choice once per
	// period of virtual time, at the first step after each period ends,
	// rather than at every scheduling step. A per-step rate means more
	// crashes per second the busier the system: under load, steps per
	// second grow a hundredfold. Set it for load runs. Per step stays the
	// default because no one period fits every system: the examples' runs
	// last from 14 ms (streams) to 80 s (checkout) of virtual time, and a
	// 100 ms period would give the fast ones no crash at all.
	NemesisEvery time.Duration
	// Check makes the scheduler verify its own invariants at every step
	// and panic on a violation (also on with GOTOGETHER_CHECK=1). For tests of
	// qenv and of code that extends it; it costs O(tasks) per step.
	Check bool
	// Clocks are the processes' clock faults (clock.go): a skew, jumps
	// and a drift, each left out when zero, as by default. Set them before
	// creating processes; a change after panics.
	Clocks Clocks
	// Stalls are pauses and gray (slow) failures of the processes, as
	// nemeses (stall.go): none by default. Set them before creating
	// processes.
	Stalls Stalls
	// Hang is how long Run waits, in wall time, for a task it woke to
	// reach its next seam (New sets 30 s; zero turns the watchdog off).
	// A task blocked outside the simulation (a real channel, a real lock,
	// database/sql's pool waiting for a connection at SetMaxOpenConns)
	// can never reach one, because the task that would unblock it is
	// parked waiting for its turn: without the watchdog the test hangs
	// until go test's timeout, naming nothing. With it, Run panics naming
	// the task and its stack. Not on the native runtime, which runs in
	// real time and has no single task to wait for.
	//
	// It measures wall time, so it can fire on a task that is not
	// blocked: one step of real work longer than Hang (raise it), or a
	// jump of the wall clock while a task runs, as a debugger's
	// breakpoint, SIGSTOP or a suspended machine make. Set Hang = 0 to
	// debug a simulation.
	Hang time.Duration

	c         Chooser
	base      context.Context   // every task's context derives from it
	names     *hooks.FaultNames // its run's fault names (base's), or nil
	nextNem   time.Time         // with NemesisEvery: when the next nemesis choice is due
	lastNow   time.Time         // Check: time never goes backwards
	creator   int64             // the goroutine that created it (owners)
	doubles   map[string]bool   // the doubles' fault-kind prefixes (claim)
	ended     []int64           // its ended tasks' goroutines (owners.ended), under owners' lock
	forgotten bool              // closed: ended tasks are no longer kept, under owners' lock
	now       time.Time
	start     time.Time
	tasks     []*task // every task, in creation order (task id-1)
	// the choice being made, while the scheduler or a nemesis makes it,
	// whose options ChoiceOptions names: a scheduling choice among optN
	// runnable tasks (optCands the predicate waiters among them), or a
	// family's nemeses (optFam). (A closure made at every step cost a step
	// an allocation.)
	optSched  bool
	optN      int
	optCands  []*task
	optFam    *nemFam
	fams      []nemFam  // the nemesis families, and their buffers (maybeNemesis)
	famOrder  []int     // the families with a nemesis enabled at this step, in order
	logLens   []logLen  // maybeNemesis's buffer
	cands     []*task   // runnable's buffer of predicate waiters
	checking  []*task   // the tasks selfCheck looks at: every task but those it saw ended and unqueued (Check)
	runq      fenwick   // the queued runnable tasks, by id: k-th in O(log n)
	pred      []*task   // tasks waiting on a predicate (evaluated every step)
	timers    timerHeap // deadlines of waiting tasks
	nextID    int
	procs     []*Process
	net       Network // the network between processes (Net)
	clocksSet *Clocks // Clocks as the first process saw them
	nemeses   []*nemesis
	held      []*task // runnable tasks of stalled processes (Stalls)
	stallsSet *Stalls // Stalls when the first process was made: a change after panics
	allFaults bool    // GOTOGETHER_ALL_FAULTS: every process partitionable from every other
	onCrash   []func(*Process)
	onLife    []func(*Process) // after a process's termination, crash or restart (Router)
	keyed     map[any]any      // keyed locks and wait groups (keyed.go)
	faultsOff bool
	settling  bool
	ran       int               // the id of the task that ran last (a settle's turn)
	core      map[*Process]bool // during SettleCore: the processes faults leave alone
	steps     int
	panicked  any
	stack     []byte
	closed    bool
	nat       *native              // the native runtime (native.go), if it runs on one
	hangOn    bool                 // Hang's watchdog runs (watch)
	awaited   atomic.Pointer[task] // the task the scheduler last woke, for the watchdog
	hangMsg   string               // the watchdog's panic, written before its sentinel
	hung      *task                // the task the watchdog abandoned: Close leaves it
}

type task struct {
	sim       *Sim
	id        int // creation order: the order runnable tasks are offered in
	name      string
	proc      *Process
	inc       int       // the process incarnation it belongs to
	held      bool      // runnable but held while its process stalls (Stalls): off the run queue
	heldUntil time.Time // when it is released
	wake      chan struct{}
	state     atomic.Int64    // running or parked, and its wakes (wake, handBack): the watchdog's view
	wakes     int64           // its wakes so far (the scheduler's)
	back      chan struct{}   // it hands control back to the scheduler (its own: a hung task's late hand-back reaches no other wait)
	gid       atomic.Int64    // its goroutine's id, for the stack the watchdog reports (Hang), from the watchdog's goroutine
	ctx       context.Context // its context (Current)
	waiting   bool            // parked until ready, a signal, or deadline
	ready     func() bool     // the predicate waited for, if any
	deadline  time.Time       // zero: none
	gen       int             // bumped when a wait ends: stale timers are skipped
	notified  bool            // a Signal it waited on fired
	queued    bool            // in runq
	done      bool
	killed    bool
	daemon    bool // its waits may last forever (Daemon)
	idle      bool // this wait may last forever (a double's receive)
}

type timer struct {
	at  time.Time
	id  int // tie-break: creation order
	t   *task
	gen int
}

type timerHeap []timer

func (h timerHeap) Len() int { return len(h) }
func (h timerHeap) Less(i, j int) bool {
	if !h[i].at.Equal(h[j].at) {
		return h[i].at.Before(h[j].at)
	}
	return h[i].id < h[j].id
}
func (h timerHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *timerHeap) Push(x any)   { *h = append(*h, x.(timer)) }
func (h *timerHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

type nemesis struct {
	name    string
	family  string // its choice's: "fault.nemesis.<family>" (nemesisFamily)
	enabled func() bool
	do      func()
	procs   []*Process // the processes it touches: a core settle keeps it if one is outside the core
}

// beyond reports whether n touches a process outside core.
func (n *nemesis) beyond(core map[*Process]bool) bool {
	for _, p := range n.procs {
		if !core[p] {
			return true
		}
	}
	return false
}

// nemesisFamily is the family a nemesis is chosen in: its name's first
// segment ("crash.payments": crash), a heal with the partitions it ends.
func nemesisFamily(name string) string {
	f, _, _ := strings.Cut(name, ".")
	if f == "heal" {
		return "partition"
	}
	return f
}

// Epoch is the virtual time a simulation starts at.
// Read-only.
var Epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// allFaultsClocks and allFaultsStalls are the clock faults and stalls
// every simulation has under GOTOGETHER_ALL_FAULTS=1, which also makes
// the network between every two processes a nemesis (PartitionNemesis):
// every fault family on, for running a suite's explorations with the
// faults it was written before (a family added later arrives
// opt-in, and the code written before it is never tried against it). A
// simulation may set its own after New. Read-only.
var (
	allFaultsClocks = Clocks{Skew: 100 * time.Millisecond, Jump: time.Second, Drift: 0.01}
	allFaultsStalls = Stalls{Pause: 200 * time.Millisecond, Slow: 5 * time.Millisecond, SlowFor: 500 * time.Millisecond}
)

// New returns a simulation whose choices c makes.
func New(c Chooser) *Sim {
	base := context.Background()
	if ctx, ok := choice.Context(c); ok { // through any wrapper
		base = ctx
	} else if rc, ok := qrun.Current(); ok {
		base = rc // the run it is created in (qrun.Start, an enclosing simulation)
	}
	env, resolved := hooks.SimEnvironmentOf(base)
	if !resolved {
		env = hooks.SimEnvironment{AllFaults: os.Getenv("GOTOGETHER_ALL_FAULTS") == "1", Check: os.Getenv("GOTOGETHER_CHECK") == "1"}
	}
	s := &Sim{c: c, base: base, names: hooks.FaultNamesOf(base), now: Epoch, start: Epoch, MaxSteps: 100000,
		Check: env.Check, creator: goid(), Hang: 30 * time.Second}
	if s.names == nil {
		// a simulation is a run, whose declarations are its own
		// (DeclareFaultSite's sites, FaultSites) even outside qtest
		s.names = &hooks.FaultNames{}
		s.base = hooks.WithFaultNames(s.base, s.names)
	}
	s.net.sim = s
	if env.AllFaults {
		s.Clocks, s.Stalls, s.allFaults = allFaultsClocks, allFaultsStalls, true
	}
	own(s.creator, s, nil)
	live.Add(1)
	if o, ok := choice.As[simObserver](c); ok {
		o.ObserveSim(s)
	}
	return s
}

// A simObserver is a chooser that reads the simulation it chooses for
// while it chooses (ChoiceOptions, Tasks): qtest's run navigator. New
// hands it the simulation made from it.
type simObserver interface{ ObserveSim(*Sim) }

// ChoiceOptions names the options of the choice s is making, for a
// chooser to read from its Choose (one New handed the simulation): a
// scheduling choice's runnable tasks, option i the i-th; a nemesis
// family's, "none" then the nemeses enabled; a declared fault kind's,
// "nominal" then its options. nil for another choice, or outside one.
func (s *Sim) ChoiceOptions(kind string) []string {
	if s.optSched {
		ids := s.runnableIDs(s.optN, s.optCands)
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = s.tasks[id-1].name
		}
		return out
	}
	if s.optFam != nil {
		return append([]string{"none"}, s.optFam.names()...)
	}
	if IsFault(kind) {
		if o := choice.Options(kind); o != nil {
			return append([]string{"nominal"}, o...)
		}
	}
	return nil
}

// Choose makes a choice of the environment. While the simulation settles,
// fault choices are nominal.
func (s *Sim) Choose(kind string, n int) int {
	if n <= 1 || s.settling || (s.faultsOff && IsFault(kind)) {
		return 0
	}
	return s.draw(kind, n)
}

// draw asks the Chooser.
func (s *Sim) draw(kind string, n int) int {
	if IsFault(kind) {
		choice.Seen(kind) // a kind, declared or not
	}
	v := s.c.Choose(kind, n)
	if v < 0 || v >= n {
		panic(fmt.Sprintf("qenv: chooser returned %d for %s with %d options", v, kind, n))
	}
	if v != 0 && IsFault(kind) && s.Log != nil {
		s.Log(fmt.Sprintf("%v %s=%d", s.Elapsed(), kind, v))
	}
	return v
}

// fault makes a fault choice: whether (and which) fault of a kind happens
// here. 0 is nominal. Doubles declare their kinds and choose with FaultOf.
func (s *Sim) fault(kind string, n int) int { return s.faultAt(nil, kind, n) }

// faultAt is a fault choice at a double of process owner (nil: none): in a
// core settle, made as in a run if it touches a process outside the core.
func (s *Sim) faultAt(owner *Process, kind string, n int) int {
	if n > 1 && s.settling && s.beyondCore(owner) {
		return s.draw("fault."+kind, n)
	}
	return s.Choose("fault."+kind, n)
}

// beyondCore reports whether a fault at a double of process owner touches
// a process outside a core settle's core: the double's, or that of the
// task making the choice.
func (s *Sim) beyondCore(owner *Process) bool {
	if s.core == nil {
		return false
	}
	if owner != nil && !s.core[owner] {
		return true
	}
	t := taskOf(nil)
	return t != nil && t.sim == s && t.proc != nil && !s.core[t.proc]
}

// Now is the virtual time (on the native runtime, the time package's).
func (s *Sim) Now() time.Time {
	if s.nat != nil {
		return nativeNow()
	}
	return s.now
}

// ToVirtual translates a time read from the real clock into virtual time,
// as if the real clock had advanced with the simulation since. Libraries
// that read the real clock (time.Now in code qenv cannot reach) write their
// timestamps through a seam that translates them with ToVirtual, and read
// them back with ToReal: their time.Since then measures virtual time.
func (s *Sim) ToVirtual(real time.Time) time.Time {
	if s.nat != nil {
		return real // the library's clock is the simulation's
	}
	return s.now.Add(real.Sub(time.Now())) //qenv:ok translating the real clock at a seam (ToVirtual)
}

// ToReal translates a virtual time into the real clock's frame (the inverse
// of ToVirtual, as of now).
func (s *Sim) ToReal(virtual time.Time) time.Time {
	if s.nat != nil {
		return virtual
	}
	return time.Now().Add(virtual.Sub(s.now)) //qenv:ok translating to the real clock at a seam (ToReal)
}

// Context is the context every task derives from (see WithContext): for
// code that runs outside any task, such as a scenario's final step.
func (s *Sim) Context() context.Context { return s.base }

// Elapsed is the virtual time since the start.
func (s *Sim) Elapsed() time.Duration { return s.Now().Sub(s.start) }

// ---- tasks -------------------------------------------------------------------

type ctxKey struct{}

// taskOf is the simulation task ctx belongs to; for a context that
// carries none (context.Background() in a library, or in code woven by
// qenv/weave), the task running on the calling goroutine. So Sleep, Wait
// and Go called with any context from a task stay in its simulation:
// virtual time, not real.
func taskOf(ctx context.Context) *task {
	if ctx != nil {
		if t, ok := ctx.Value(ctxKey{}).(*task); ok {
			return t
		}
	}
	if live.Load() == 0 {
		return nil
	}
	if o, ok := ownerOf(goid()); ok {
		return o.t
	}
	return nil
}

// From returns the simulation ctx runs in, or nil outside one.
func From(ctx context.Context) *Sim {
	if t := taskOf(ctx); t != nil {
		return t.sim
	}
	return nil
}

// Go starts fn as a task of the simulation, or, outside one, as a
// goroutine. A task started from another task belongs to the same process
// and dies with it.
func Go(ctx context.Context, name string, fn func(ctx context.Context)) {
	t := taskOf(ctx)
	if t == nil {
		go fn(ctx) //qenv:ok outside a simulation, Go is a goroutine
		return
	}
	t.sim.spawn(ctx, name, t.proc, fn)
}

// Spawn starts a task outside any process (a client, a test driver).
func (s *Sim) Spawn(name string, fn func(ctx context.Context)) {
	s.spawn(s.base, name, nil, fn)
}

// Spawn starts a task in process p, from outside it (a test driver giving
// a node work): it belongs to p's current incarnation, as a task p's own
// code starts with Go does, so a crash kills it and partitions cut it
// off. A process that is down has no incarnation to start it in: it
// panics.
func (p *Process) Spawn(name string, fn func(ctx context.Context)) {
	if p.down {
		panic(fmt.Sprintf("qenv: %s.Spawn(%q): the process is down; spawn from an OnCrash hook after Restart, or from its start function", p.Name, name))
	}
	p.sim.spawn(p.sim.base, p.Name+"."+name, p, fn)
}

func (s *Sim) spawn(parent context.Context, name string, p *Process, fn func(ctx context.Context)) {
	s.nextID++
	t := &task{sim: s, id: s.nextID, name: name, proc: p, wake: make(chan struct{}), back: make(chan struct{})}
	if p != nil {
		t.inc = p.Incarnation
	}
	s.tasks = append(s.tasks, t)
	s.checking = append(s.checking, t) // (whether Check is on yet or not: it may be set later)
	ctx := qrun.With(context.WithValue(parent, ctxKey{}, t), runkeys.Task, any(t))
	t.ctx = ctx
	if s.nat != nil {
		s.spawnNative(t, fn)
		return
	}
	s.enqueue(t)
	go func() { //qenv:ok the task's goroutine, which the scheduler runs one at a time
		id := goid()
		t.gid.Store(id)
		own(id, s, t)
		defer disown(id, s)
		<-t.wake
		defer func() {
			r := recover()
			if !t.handBack() {
				return // abandoned by the watchdog (Hang): nothing of the simulation's is its to touch
			}
			if r != nil {
				if _, ok := r.(killed); !ok {
					// re-raised by Run with its own value: a test
					// framework's abort (Hegel's) must reach it intact
					t.sim.notePanic(r, debug.Stack())
					if t.sim.Log != nil {
						t.sim.Log(fmt.Sprintf("task %s panicked: %v\n%s", t.name, r, t.sim.stack))
					}
				}
			}
			t.done = true
			t.sim.unqueue(t)
			t.back <- struct{}{}
		}()
		if t.killed {
			panic(killed{})
		}
		fn(ctx)
	}()
}

type killed struct{}

// Unwinding reports whether r, recovered from a panic, is the one qenv
// unwinds a killed task with (a crashed process's tasks, every task left
// at Close). An adapter whose caller holds a lock it releases only on a
// normal return (database/sql's Tx.QueryContext holds the Tx's closemu)
// recovers it and returns an error instead, so the caller's deferred
// cleanup does not wait on that lock forever; the task is unwound again
// at its next seam. Any other panic it re-raises.
func Unwinding(r any) bool {
	_, ok := r.(killed)
	return ok
}

// Yield lets the scheduler run another task. Seams call it; code outside a
// simulation is unaffected.
func Yield(ctx context.Context) {
	if t := taskOf(ctx); t != nil {
		t.park()
	}
}

// park hands control back to the scheduler until the task is picked again.
// A task that is not waiting (Yield) is runnable at once.
func (t *task) park() {
	if t.sim.nat != nil {
		t.yieldNative()
		return
	}
	if t.killed && t.sim.closed {
		// a seam in code running as Close unwinds the task (a deferred
		// call, or code after an adapter recovered the unwind): handing
		// back here would end Close's wait for it early and leave it
		// parked forever
		panic(killed{})
	}
	if !t.handBack() {
		panic(killed{}) // abandoned by the watchdog: it unwinds, touching nothing of the simulation's
	}
	if !t.waiting {
		t.sim.enqueue(t)
	}
	t.back <- struct{}{}
	<-t.wake
	if t.killed {
		panic(killed{})
	}
}

// wait parks the task until ready holds (nil: no predicate), a Signal
// wakes it, or deadline passes (zero: none).
func (t *task) wait(ready func() bool, deadline time.Time) {
	s := t.sim
	if s.nat != nil {
		t.waitNative(ready, deadline)
		t.idle = false
		return
	}
	var waited func()
	if s.Profile != nil {
		waited = s.Profile.Waited(t.name, s.now, func() time.Time { return s.now })
	}
	t.waiting, t.ready, t.deadline, t.notified = true, ready, deadline, false
	if ready != nil {
		s.pred = append(s.pred, t)
	}
	if !deadline.IsZero() {
		heap.Push(&s.timers, timer{at: deadline, id: t.id, t: t, gen: t.gen})
	}
	t.park() // a killed task does not return: its wait is not counted
	t.idle = false
	if waited != nil {
		waited()
	}
	t.ready, t.deadline = nil, time.Time{}
}

// enqueue makes a task runnable.
func (s *Sim) enqueue(t *task) {
	if t.waiting {
		t.waiting = false
		t.gen++
	}
	if s.nat != nil {
		if s.nat.coop {
			s.nat.release() // wake it now: the notifier holds no lock
		}
		return // it wakes when the notifier gives up the lock
	}
	if !t.queued && !t.done && !t.killed && !t.held {
		if until := s.holdUntil(t); until.After(s.now) {
			s.hold(t, until) // its wake stands; it runs when its process goes on
			return
		}
		t.queued = true
		s.runq.add(t.id, 1, s)
	}
}

// unqueue takes a task off the run queue (it runs, finished or was killed).
func (s *Sim) unqueue(t *task) {
	if t.queued {
		t.queued = false
		s.runq.add(t.id, -1, s)
	}
}

// fenwick counts queued task ids, to find the k-th runnable task (in
// creation order) in O(log n) however many tasks are runnable.
type fenwick struct{ tree []int }

func (f *fenwick) add(id, d int, s *Sim) {
	if id >= len(f.tree) {
		f.grow(id, s) // counts the tasks already flagged queued, this one included
		return
	}
	for i := id; i < len(f.tree); i += i & -i {
		f.tree[i] += d
	}
}

// grow rebuilds the tree for ids up to at least id, from the tasks' queued
// flags.
func (f *fenwick) grow(id int, s *Sim) {
	n := 2 * len(f.tree)
	if n <= id {
		n = id + 1
	}
	f.tree = make([]int, n)
	for _, t := range s.tasks {
		if t.queued {
			for i := t.id; i < n; i += i & -i {
				f.tree[i]++
			}
		}
	}
}

// count is the number of queued ids <= id.
func (f *fenwick) count(id int) int {
	if id >= len(f.tree) {
		id = len(f.tree) - 1
	}
	c := 0
	for i := id; i > 0; i -= i & -i {
		c += f.tree[i]
	}
	return c
}

func (f *fenwick) total() int { return f.count(len(f.tree) - 1) }

// kth is the id of the k-th (0-based) queued id.
func (f *fenwick) kth(k int) int {
	pos, step := 0, 1
	for step*2 < len(f.tree) {
		step *= 2
	}
	for ; step > 0; step /= 2 {
		if next := pos + step; next < len(f.tree) && f.tree[next] <= k {
			pos = next
			k -= f.tree[next]
		}
	}
	return pos + 1
}

// Wait blocks the task until ready holds. ready is evaluated by the
// scheduler, between tasks, and must only read simulation state. Outside a
// simulation it polls ready in real time (every millisecond) until it
// holds or ctx is done: ready must then be safe to call concurrently.
func Wait(ctx context.Context, ready func() bool) {
	waitUntil(ctx, ready, time.Time{})
}

// waitUntil blocks until ready holds or the virtual time reaches deadline
// (zero: no deadline): the simulation's true time (Sim.Now), not a
// process's clock; WaitFor waits an interval on the caller's clock. It reports whether ready held. Outside a
// simulation the deadline is real time, as in Wait.
func waitUntil(ctx context.Context, ready func() bool, deadline time.Time) bool {
	t := taskOf(ctx)
	if t == nil {
		return pollReal(ctx, ready, deadline)
	}
	if ready() {
		return true
	}
	t.wait(ready, deadline)
	return ready()
}

// A Signal wakes the tasks waiting on it. Waiting on a Signal costs the
// scheduler nothing until Notify, unlike Wait's predicate, which it
// evaluates at every step: use Signals where many tasks wait (requests
// waiting for responses, workers waiting for work).
type Signal struct {
	waiters []waiter

	// outside a simulation: a channel Notify closes
	mu sync.Mutex
	ch chan struct{}
}

// pollReal is Wait outside a simulation.
func pollReal(ctx context.Context, ready func() bool, deadline time.Time) bool {
	tick := time.NewTicker(time.Millisecond) //qenv:ok the real wait, outside any simulation
	defer tick.Stop()
	for !ready() {
		if !deadline.IsZero() && !time.Now().Before(deadline) { //qenv:ok the real wait's deadline
			return false
		}
		select { //qenv:ok outside a simulation: a real poll, until the context ends
		case <-ctx.Done():
			return ready()
		case <-tick.C:
		}
	}
	return true
}

// waiter is a task's wait on a Signal: gen tells it from the task's later
// waits, which a stale entry (the wait timed out) must not end.
type waiter struct {
	t   *task
	gen int
}

// Wait blocks until Notify or d has passed (0: no limit; a negative d has
// passed already, as for time.After, and times out at once), in the
// simulation's true time (Sim.Now), not a process's clock: WaitFor's
// interval is the caller's clock's. It reports whether Notify woke it.
// Outside a simulation it waits in real time or until ctx is done.
func (g *Signal) Wait(ctx context.Context, d time.Duration) bool {
	t := taskOf(ctx)
	if t == nil {
		var deadline time.Time
		if d != 0 {
			deadline = time.Now().Add(max(d, 0)) //qenv:ok the real wait's deadline, outside any simulation
		}
		return g.waitReal(ctx, deadline)
	}
	var deadline time.Time
	if d != 0 {
		deadline = t.sim.Now().Add(max(d, 0))
	}
	g.waiters = append(g.waiters, waiter{t, t.gen})
	t.wait(nil, deadline)
	return t.notified
}

func (g *Signal) waitReal(ctx context.Context, deadline time.Time) bool {
	g.mu.Lock()
	if g.ch == nil {
		g.ch = make(chan struct{})
	}
	ch := g.ch
	g.mu.Unlock()
	var timeout <-chan time.Time
	if !deadline.IsZero() {
		tm := time.NewTimer(time.Until(deadline)) //qenv:ok the real wait's deadline
		defer tm.Stop()
		timeout = tm.C
	}
	select { //qenv:ok outside a simulation: a real wait, its notify, timeout or context
	case <-ch:
		return true
	case <-timeout:
	case <-ctx.Done():
	}
	return false
}

// Notify wakes every task waiting on the signal (and every goroutine
// waiting outside a simulation).
func (g *Signal) Notify() {
	g.mu.Lock()
	if g.ch != nil {
		close(g.ch)
		g.ch = nil
	}
	g.mu.Unlock()
	// tasks wait in a simulation, one task running at a time; outside one
	// there are none, and goroutines notify concurrently (a real store's
	// commits): resetting the empty list was a write every one of them
	// raced on
	if len(g.waiters) == 0 {
		return
	}
	for _, w := range g.waiters {
		if t := w.t; t.waiting && t.gen == w.gen && !t.done && !t.killed {
			t.notified = true
			t.sim.enqueue(t)
		}
	}
	g.waiters = g.waiters[:0]
}

// Now is the time on the wall clock of ctx's process in a simulation: the
// virtual time, off by the process's clock faults (Sim.Clocks). A Time it
// returns carries no monotonic reading, so Now(ctx).Sub is wall
// arithmetic, which a jump moves: time intervals with Mono and Since, as
// time.Since does in Go. Outside a simulation it is the real time.
func Now(ctx context.Context) time.Time {
	if t := taskOf(ctx); t != nil {
		return t.sim.clockOf(t)
	}
	return time.Now() //qenv:ok outside a simulation, the wall clock
}

// Sleep waits d of virtual time in a simulation, else of real time (or
// until ctx is done).
func Sleep(ctx context.Context, d time.Duration) {
	t := taskOf(ctx)
	if t == nil {
		select { //qenv:ok outside a simulation, real time
		case <-time.After(d): //qenv:ok outside a simulation, real time
		case <-ctx.Done():
		}
		return
	}
	d = t.proc.drifted(d)
	if t.sim.nat != nil {
		t.sleepNative(d)
		return
	}
	t.wait(nil, t.sim.now.Add(d))
}

// ---- processes ---------------------------------------------------------------

// A Process is a group of tasks that crash together. Its start function
// is its main task, run on boot and again after every restart, with
// Incarnation one higher: what a restarted process remembers is what start
// rebuilds from durable components (a Store), nothing else.
type Process struct {
	Name        string
	Incarnation int
	// Version is the code it runs: "" as created, then what RestartAs or
	// an upgrade (Sim.Upgrades) restarted it as
	Version string
	sim     *Sim
	stall   stall // its pause and slowness (Stalls)
	start   func(ctx context.Context)
	down    bool
	machine Machine       // native, with a Platform: the process's machine
	queue   chan func()   // native, with a Platform: tasks to start there
	clock   time.Duration // its wall clock's skew and jumps (Clocks)
	rate    float64       // its clocks' drift: + fast, - slow (Clocks.Drift)
	born    time.Time     // when its clock started drifting
	// its termination (terminate.go): its registration, whether it was
	// sent the signal, the contexts Terminating gave its tasks, and
	// whether its next crash is its own exit
	term        *termination
	terminating bool
	termCancels []termCancel
	exiting     bool
}

// ID is the process name and incarnation, e.g. "payments#2".
func (p *Process) ID() string { return fmt.Sprintf("%s#%d", p.Name, p.Incarnation) }

// Down reports whether the process is crashed and not yet restarted.
func (p *Process) Down() bool { return p.down }

// Process creates and boots a process. If crashable, the scheduler may
// crash it at any step (a nemesis, "nemesis.crash.<name>" in a Script), and restart it after
// the crash.
func (s *Sim) Process(name string, crashable bool, start func(ctx context.Context)) *Process {
	p := &Process{Name: name, Incarnation: 1, sim: s, start: start}
	s.procs = append(s.procs, p)
	s.clockProcess(p)
	if s.nat != nil && s.nat.plat != nil {
		p.queue = make(chan func(), spawnQueue)
		p.machine = s.nat.plat.Machine(name, s.machineMain(p))
	}
	s.spawn(s.base, name+".main", p, start)
	if crashable {
		s.nemesisOf("crash."+name, []*Process{p}, func() bool { return !p.down }, func() { p.Crash(); p.Restart() })
	}
	s.stallNemeses(p) // a pause or a slowdown is no crash: any process may suffer one
	if s.allFaults {
		for _, q := range s.procs[:len(s.procs)-1] {
			s.net.PartitionNemesis(q, p)
		}
	}
	return p
}

// ProcessOf returns the process the task ctx runs in, or nil.
func ProcessOf(ctx context.Context) *Process {
	if t := taskOf(ctx); t != nil {
		return t.proc
	}
	return nil
}

// Crash kills every task of the process: none runs again, and no deferred
// code of theirs runs while the simulation runs (as in a real crash; they
// unwind at Close, and steps they record then are only Interrupted). A
// task of the process calling Crash stops in it. Components holding state
// for the process (open transactions, unacknowledged deliveries) release
// it through their OnCrash hooks; a supervisor restarts it from one, or
// from another process's task.
func (p *Process) Crash() {
	if p.down {
		return
	}
	p.down = true
	p.stall = stall{} // a restarted process starts fresh, not paused
	for _, t := range p.sim.tasks {
		if t.proc == p && !t.done {
			t.killed = true
			if p.sim.nat == nil {
				p.sim.unqueue(t)
			}
		}
	}
	what := "crash"
	if p.exiting {
		what, p.exiting = "exit", false
	}
	if p.sim.Log != nil {
		p.sim.Log(fmt.Sprintf("%v %s %s", p.sim.Elapsed(), what, p.ID()))
	}
	p.termCancels = nil // its incarnation's, which no task of it reads again
	if p.sim.nat != nil {
		t := taskOf(nil)
		self := t != nil && t.proc == p && t.sim == p.sim
		if p.machine != nil {
			p.machine.Crash() // before the hooks, which may restart it
		}
		p.sim.changed(p)
		for _, h := range p.sim.onCrash {
			h(p)
		}
		if self {
			p.stopSelfNative(t)
		}
		return
	}
	p.sim.changed(p) // before the hooks, which may restart it
	for _, h := range p.sim.onCrash {
		h(p)
	}
	// A task of the process crashing it dies here too: hand control back
	// and stay frozen until Close, like the others, rather than run on to
	// its next scheduling point.
	if t := taskOf(nil); t != nil && t.proc == p && t.sim == p.sim {
		if !t.handBack() {
			panic(killed{})
		}
		t.back <- struct{}{}
		<-t.wake
		panic(killed{})
	}
}

// Restart boots a crashed process as a new incarnation.
func (p *Process) Restart() {
	if !p.down {
		return
	}
	p.down = false
	p.terminating = false // a new incarnation, never signalled
	p.Incarnation++
	if p.sim.nat != nil {
		p.restartNative()
	}
	p.sim.spawn(p.sim.base, p.Name+".main", p, p.start)
	p.sim.changed(p)
}

// RestartAs boots a crashed process as a new incarnation running start,
// as version: an upgrade (or a rollback) of its code. What the new code
// reads of the old's is what the durable components hold, as for a
// Restart; the same model judges every version. A process that is up is
// left as it is.
func (p *Process) RestartAs(version string, start func(ctx context.Context)) {
	if !p.down {
		return
	}
	p.as(version, start)
	p.Restart()
}

// as makes start, as version, what the process runs from its next boot.
func (p *Process) as(version string, start func(ctx context.Context)) {
	p.start, p.Version = start, version
	if p.sim.Log != nil {
		p.sim.Log(fmt.Sprintf("%v upgrade %s to %q", p.sim.Elapsed(), p.Name, version))
	}
}

// Upgrade crashes the process and restarts it as version, running start:
// one step of a rolling upgrade, made where a test wants it. The new code
// is in place before the crash, so a supervisor that restarts the process
// from an OnCrash hook boots it. A process that is down is restarted as
// version. A task of the process cannot upgrade it (it stops in its own
// crash): Upgrade panics there with the fix.
func (p *Process) Upgrade(version string, start func(ctx context.Context)) {
	if t := taskOf(nil); t != nil && t.proc == p && t.sim == p.sim {
		panic(fmt.Sprintf("qenv: %s upgrades itself: a task of a process stops in its own crash, before it could restart; "+
			"upgrade it from another process's task (a supervisor's), or register the rollout with Sim.Upgrades", p.Name))
	}
	p.as(version, start)
	p.Crash()
	p.Restart() // unless a hook did
}

// Upgrades registers a rolling upgrade to version as nemeses, one per
// process ("nemesis.upgrade.<name>", family upgrade): each, enabled while
// its process runs another version and is up, crashes it and restarts it
// as version running start. The scheduler then upgrades the processes at
// any step, in any order, at the nemesis rate ("nemesis.upgrade"), so
// runs pass through every mix of versions; a Script names one
// ("nemesis.upgrade.b") and Exhaust bounds them as faults. Faults stay
// off after a settle, so a rollout a test needs finished calls Upgrade
// before it settles.
func (s *Sim) Upgrades(version string, start func(ctx context.Context), procs ...*Process) {
	for _, p := range procs {
		for _, n := range s.nemeses {
			if n.name == "upgrade."+p.Name {
				// two would undo each other, and no Script or recorded choice
				// could tell them apart
				panic(fmt.Sprintf("qenv: a second rollout of %s (to %q): one rollout a process a run; for v2 then v3, call Process.Upgrade where the test wants each", p.Name, version))
			}
		}
		s.nemesisOf("upgrade."+p.Name, []*Process{p}, func() bool { return !p.down && p.Version != version },
			func() { p.Upgrade(version, start) })
	}
}

// OnCrash registers a hook run when a process crashes.
func (s *Sim) OnCrash(h func(*Process)) { s.onCrash = append(s.onCrash, h) }

// Nemesis registers a fault the scheduler may inject at any step while
// enabled holds (a crash, a partition). Its family is its name's first
// segment ("crash.payments": crash; a heal is a partition's): the choice is
// "fault.nemesis.<family>", among the family's enabled nemeses, at that
// kind's rate ("nemesis.crash"; "nemesis" covers every family), so one
// family's nemeses do not dilute another's.
func (s *Sim) Nemesis(name string, enabled func() bool, do func()) {
	s.nemesisOf(name, nil, enabled, do)
}

// nemesisOf registers a nemesis that touches procs (SettleCore).
func (s *Sim) nemesisOf(name string, procs []*Process, enabled func() bool, do func()) {
	choice.DeclareNemesis(s.names, name)
	s.nemeses = append(s.nemeses, &nemesis{name: name, family: nemesisFamily(name), enabled: enabled, do: do, procs: procs})
}

// ---- running -----------------------------------------------------------------

// ErrStuck is returned by Run when no task can run, no deadline is set,
// and a task that is not a daemon waits: nothing will ever wake it. The
// error lists the waiting tasks (Sim.Tasks).
var ErrStuck = errors.New("qenv: stuck")

// Daemon marks the calling task as one whose waits may last forever (a
// server's accept loop, a worker waiting for work): Run does not report
// it as stuck. Outside a simulation it does nothing. The doubles' own
// receives (Queue.Receive) need no mark.
func Daemon(ctx context.Context) {
	if t := taskOf(ctx); t != nil {
		t.daemon = true
	}
}

// WaitIdle is Wait for a wait that may last forever: a double's receive
// (Queue.Receive, a log's poll), whose caller has nothing to do until a
// message comes. Run does not report a task in it as stuck.
func WaitIdle(ctx context.Context, ready func() bool) {
	t := taskOf(ctx)
	if t == nil {
		panic("qenv: Wait outside a simulation")
	}
	if ready() {
		return
	}
	t.idle = true
	t.wait(ready, time.Time{})
}

// stuck lists the tasks waiting with no deadline that are neither daemons
// nor in an idle wait ("": none).
func (s *Sim) stuck() string {
	var lines []string
	for _, t := range s.tasks {
		if t.done || t.killed || !t.waiting || t.daemon || t.idle || !t.deadline.IsZero() {
			continue
		}
		if t.proc != nil && t.proc.down {
			continue
		}
		lines = append(lines, "  "+t.name+": waiting")
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// ErrMaxSteps is returned by Run when a run takes more than MaxSteps steps.
var ErrMaxSteps = errors.New("qenv: too many scheduling steps (livelock?)")

// Run schedules tasks until none can run and no deadline is within d of
// virtual time (the system is quiet), or the virtual time has advanced by
// d. Tasks still waiting then (workers waiting for messages) stay parked.
// Run(0) is no unbounded run: it runs what can run at the current
// instant (deadlines that fall on it included) and returns without
// advancing virtual time, a way to let tasks just spawned reach their
// first wait; give a horizon to let time pass, or Settle to run until
// quiet. On the native runtime Run(d) sleeps d of real time.
// A panic in a task is re-raised here, with its own value (PanicStack has
// the task's stack).
func (s *Sim) Run(d time.Duration) (err error) {
	if finish := hooks.ObserveSimulation(s.base, s, hooks.Simulation{Native: s.nat != nil, Horizon: d, MaxSteps: s.MaxSteps, Hang: s.Hang, StepsBefore: s.steps}); finish != nil {
		defer func() { finish(s.steps, errors.Is(err, ErrMaxSteps), s.hung != nil) }()
	}
	s.stallsFixed()
	if s.clocksSet != nil {
		s.clocksFor() // Clocks changed after the first process: panics
	}
	if s.nat != nil {
		return s.runNative(d)
	}
	defer s.watch()()
	horizon := s.now.Add(d)
	for {
		if s.steps >= s.MaxSteps {
			return ErrMaxSteps
		}
		n, cands := s.runnable()
		if n == 0 {
			next, ok := s.nextDeadline()
			if !ok {
				if st := s.stuck(); st != "" {
					return fmt.Errorf("%w at %v: no task can run and no deadline is set; the tasks waiting (a missed Notify, a lost message, a lock never released; mark a task that may wait forever with qenv.Daemon):\n%s", ErrStuck, s.now.Sub(s.start), st)
				}
			}
			if !ok || next.After(horizon) {
				if s.now.Before(horizon) && ok {
					s.now = horizon
				}
				return nil
			}
			s.now = next
			continue
		}
		s.steps++
		if s.Check {
			s.selfCheck()
		}
		if (!s.faultsOff || s.core != nil) && s.nemesisDue() {
			s.maybeNemesis()
			if n, cands = s.runnable(); n == 0 {
				continue
			}
		}
		k := 0
		if s.settling {
			k = s.turn(n, cands)
		} else if by := taskChooserOf(s.c); by != nil && n > 1 { // one runnable task is no choice: Choose asks nothing either
			k = by(s.runnableIDs(n, cands))
			if k < 0 || k >= n {
				panic(fmt.Sprintf("qenv: ChooseTask returned %d of %d runnable tasks", k, n))
			}
			eachLog(s.c, func(r *ChoiceLog) { r.Took("sched.task", n, k) }) // the choice it stands for: replays without it
		} else {
			s.optSched, s.optN, s.optCands = true, n, cands
			k = s.Choose("sched.task", n)
			s.optSched, s.optCands = false, nil
		}
		t := s.pick(k, cands)
		s.ran = t.id
		s.dequeue(t)
		s.wake(t)
		s.await(t)
		if s.panicked != nil {
			p := s.panicked
			s.panicked = nil
			panic(p)
		}
	}
}

// Settle turns faults off and runs until quiet or d has passed: the system's chance to
// converge, after which properties conditioned on quiescence apply. It
// draws nothing from the Chooser: what a run explores is what happened
// before it. It takes runnable tasks in turn (a fair schedule: a task that
// spins until another acts does not starve it), and every other choice is
// 0.
//
// It returns an *Unsettled error (ErrUnsettled) if the system is not
// quiet when its time is up: a task that is no daemon is still at work or
// waiting (a retry loop that never ends, a client waiting on a reply that
// will not come while a daemon's timer keeps time moving, which once hid
// such a stall). A property conditioned on quiescence would be judged on a
// system that never reached it. Not on the native runtime.
//
// Faults stay off after it returns: what follows (a probe, an audit
// through the doubles) runs on the settled system. ResumeFaults starts a
// faulty phase again.
func (s *Sim) Settle(d time.Duration) error { return s.settle(d, nil) }

// ResumeFaults turns faults back on after a Settle or SettleCore: a run of
// phases (faults, a settle and a probe, faults again) is faulty in each.
// Without it, a phase after a settle draws no fault, whatever the Rates.
func (s *Sim) ResumeFaults() { s.faultsOff = false }

// SettleCore is Settle with faults going on outside a core of processes
// (VOPR's liveness mode): the network heals, and the core is then left
// alone while every other process may still crash, pause, run slow, jump
// its clock or be partitioned, and a fault choice that touches it (at its
// doubles, or made by its tasks) is drawn as in a run. A system that needs
// only a quorum must finish with one: SettleCore returns an *Unsettled if
// it does not, naming the core's tasks and those of no process (its
// clients) still at work; the others' work may never end. Name every
// process the core needs, a service's own included (NewService with no
// process makes one). The run's eventual properties then apply, as after
// Settle: state them of what the core must finish. On the native
// runtime, as Settle.
func (s *Sim) SettleCore(d time.Duration, core ...*Process) error {
	if len(core) == 0 {
		panic("qenv: SettleCore with no core: Settle turns faults off everywhere")
	}
	in := map[*Process]bool{}
	for _, p := range core {
		if p == nil || p.sim != s {
			panic("qenv: SettleCore: a core process of another simulation, or nil")
		}
		in[p] = true
	}
	return s.settle(d, in)
}

func (s *Sim) settle(d time.Duration, core map[*Process]bool) error {
	s.faultsOff, s.settling = true, true
	if s.nat == nil {
		s.core = core // the native runtime's own nemeses stay off
	}
	s.net.healAll() // a whole network to recover on
	defer func() { s.settling, s.core = false, nil }()
	if err := s.Run(d); errors.Is(err, ErrStuck) {
		// waits nothing will end: not settled, whether or not a daemon's
		// timer keeps time moving (which makes the same stall Unsettled)
		return &Unsettled{After: d, Tasks: s.pending(), Stuck: err}
	} else if err != nil {
		return err
	}
	if s.nat != nil {
		return nil // the native runtime runs in real time and cannot tell, as with ErrStuck: no eventual property is judged
	}
	if pending := s.pending(); len(pending) > 0 {
		return &Unsettled{After: d, Tasks: pending}
	}
	if r := qobs.From(s.base); r != nil {
		r.Settled(s.base) // the run's eventual properties apply, if nothing is recorded after it
	}
	return nil
}

// ErrUnsettled is what an *Unsettled error is (errors.Is).
var ErrUnsettled = errors.New("qenv: not settled")

// Unsettled is a Settle whose time ran out before the system was quiet:
// the tasks, not daemons, still at work or waiting then.
type Unsettled struct {
	After time.Duration
	Tasks []string
	// Stuck is the ErrStuck error when no task could run and nothing was
	// left to wake the waiting ones (Unwrap): a stall, judged as one.
	Stuck error
}

// Unwrap is Stuck.
func (u *Unsettled) Unwrap() error { return u.Stuck }

func (u *Unsettled) Error() string {
	if u.Stuck != nil {
		return fmt.Sprintf("qenv: not settled: %v", u.Stuck)
	}
	return fmt.Sprintf("qenv: not settled after %v: tasks still at work or waiting (mark one that may wait forever with qenv.Daemon; "+
		"a longer settle, or a fix, for one that should have finished):\n  %s", u.After, strings.Join(u.Tasks, "\n  "))
}

// Is reports whether target is ErrUnsettled.
func (u *Unsettled) Is(target error) bool { return target == ErrUnsettled }

// pending lists the tasks that keep the system from being quiet: live,
// no daemon, not idle in a double's receive, of no crashed process.
func (s *Sim) pending() []string {
	var out []string
	self := taskOf(nil) // a task settling the simulation is not its pending work
	for _, t := range s.tasks {
		if t == self || t.done || t.killed || t.daemon || t.idle || t.proc != nil && (t.proc.down || s.core != nil && !s.core[t.proc]) {
			continue
		}
		state := "runnable"
		if t.waiting {
			state = "waiting"
			if !t.deadline.IsZero() {
				state = fmt.Sprintf("waiting until %v", t.deadline.Sub(s.start))
			}
		}
		out = append(out, t.name+": "+state)
	}
	sort.Strings(out)
	return out
}

// runnable reports how many tasks are runnable: the queued ones (yielded,
// new, woken by a Signal or their deadline) and the predicate waiters
// whose predicate holds now, which it returns in creation order. A
// predicate waiter is only a candidate: if another task runs first and
// falsifies its predicate, it waits on (a lock taken by another
// transaction stays taken).
func (s *Sim) runnable() (int, []*task) {
	s.release()
	for len(s.timers) > 0 {
		e := s.timers[0]
		if e.t.gen != e.gen || e.t.done || e.t.killed || !e.t.waiting {
			heap.Pop(&s.timers) // stale
			continue
		}
		if s.now.Before(e.at) {
			break
		}
		heap.Pop(&s.timers)
		s.enqueue(e.t)
	}
	cands := s.cands[:0] // (valid until the next call: a step's)
	keep := s.pred[:0]
	for _, t := range s.pred {
		if t.done || t.killed || !t.waiting || t.ready == nil {
			continue
		}
		keep = append(keep, t)
		if !t.queued && !t.stalledAt(s.now) && t.ready() { // a paused process's waiter is no candidate: it checks again after
			cands = append(cands, t)
		}
	}
	for i := len(keep); i < len(s.pred); i++ {
		s.pred[i] = nil
	}
	s.pred = keep
	slices.SortFunc(cands, func(a, b *task) int { return cmp.Compare(a.id, b.id) })
	s.cands = cands
	return s.runq.total() + len(cands), cands
}

// pick returns the k-th runnable task in creation order: queued tasks and
// predicate candidates merged by id.
// turn is a settle's pick among the n runnable tasks: the first after the
// task that ran last, in id order, wrapping around. Every runnable task runs
// within n steps, so a task that spins (yields until another acts) cannot
// starve the one it waits for, as always taking the first (choice 0) did.
func (s *Sim) turn(n int, cands []*task) int {
	k := s.runq.count(s.ran)
	for _, c := range cands {
		if c.id <= s.ran {
			k++
		}
	}
	if k >= n {
		return 0
	}
	return k
}

// runnableIDs are the ids of the n runnable tasks in the order of the
// schedule choice's options (pick's): the queued ones and the predicate
// waiters, merged by id.
func (s *Sim) runnableIDs(n int, cands []*task) []int {
	ids := make([]int, 0, n)
	q, c := s.runq.total(), 0
	for j := 0; j < q || c < len(cands); {
		if c < len(cands) && (j >= q || cands[c].id < s.runq.kth(j)) {
			ids, c = append(ids, cands[c].id), c+1
		} else {
			ids, j = append(ids, s.runq.kth(j)), j+1
		}
	}
	return ids
}

func (s *Sim) pick(k int, cands []*task) *task {
	for i, c := range cands {
		pos := s.runq.count(c.id-1) + i
		if pos == k {
			return c
		}
		if pos > k {
			return s.tasks[s.runq.kth(k-i)-1]
		}
	}
	return s.tasks[s.runq.kth(k-len(cands))-1]
}

// dequeue takes the task picked to run off the run queue, ending its wait.
func (s *Sim) dequeue(t *task) {
	if t.waiting {
		t.waiting = false
		t.gen++
	}
	s.unqueue(t)
}

func (s *Sim) nextDeadline() (time.Time, bool) {
	at, ok := s.nextRelease()
	for len(s.timers) > 0 {
		e := s.timers[0]
		if e.t.gen != e.gen || e.t.done || e.t.killed || !e.t.waiting {
			heap.Pop(&s.timers)
			continue
		}
		if !ok || e.at.Before(at) {
			at, ok = e.at, true
		}
		break
	}
	return at, ok
}

// nemesisDue reports whether a nemesis is to be considered at this step:
// at every step, or with NemesisEvery once per period.
func (s *Sim) nemesisDue() bool {
	if s.NemesisEvery <= 0 {
		return true
	}
	if s.nextNem.IsZero() {
		s.nextNem = s.start.Add(s.NemesisEvery)
	}
	if s.now.Before(s.nextNem) {
		return false
	}
	// once per step, however many periods a jump of the clock skipped
	for !s.now.Before(s.nextNem) {
		s.nextNem = s.nextNem.Add(s.NemesisEvery)
	}
	return true
}

// selfCheck verifies the scheduler's invariants (Check).
func (s *Sim) selfCheck() {
	fail := func(format string, args ...any) {
		panic(fmt.Sprintf("qenv: scheduler invariant: "+format+"\n%s", append(args, s.Tasks())...))
	}
	// Only tasks still in play: a task that ended and is not queued can
	// fail no check below again (a finished task queued anew would be
	// counted by the run queue and not here, which the count check
	// reports). Every task ever spawned, at every step, made a run of many
	// short tasks (a Service's calls) quadratic.
	live := s.checking[:0]
	for _, t := range s.checking {
		if t.queued || !t.done && !t.killed {
			live = append(live, t)
		}
	}
	clear(s.checking[len(live):])
	s.checking = live
	queued := 0
	for _, t := range s.checking {
		if t.queued {
			queued++
			if t.done || t.killed {
				fail("task %s is queued but finished", t.name)
			}
			if t.waiting {
				fail("task %s is both queued and waiting", t.name)
			}
		}
	}
	if total := s.runq.total(); total != queued {
		fail("run queue holds %d tasks, %d are flagged queued", total, queued)
	}
	for _, t := range s.held {
		if t.held && (t.queued || t.waiting) {
			fail("task %s is held for its stalled process and also queued or waiting", t.name)
		}
	}
	timers := map[*task]bool{}
	for _, e := range s.timers {
		if e.t.gen == e.gen && e.t.waiting {
			timers[e.t] = true
		}
	}
	for _, t := range s.checking {
		if t.waiting && !t.done && !t.killed && !t.deadline.IsZero() && !timers[t] {
			fail("task %s waits until %v with no live timer", t.name, t.deadline.Sub(s.start))
		}
		// a task killed by a crash keeps the deadline it was waiting for, and
		// rightly never runs again
		if t.waiting && !t.done && !t.killed && !t.deadline.IsZero() && t.deadline.Before(s.now) && !t.queued {
			fail("task %s is past its deadline (%v) and not runnable", t.name, t.deadline.Sub(s.start))
		}
	}
	if s.now.Before(s.lastNow) {
		fail("time went backwards: %v after %v", s.now.Sub(s.start), s.lastNow.Sub(s.start))
	}
	s.lastNow = s.now
}

// maybeNemesis considers the enabled nemeses: one choice per family, in the
// order the families' first enabled nemeses were registered, until one
// acts (at most one a step). A chooser that picks nemeses by name (Script)
// picks among all of them, and its pick is recorded as those choices.
func (s *Sim) maybeNemesis() {
	// the enabled nemeses by family, in buffers kept from step to step (a
	// nemesis is considered at every step)
	order := s.famOrder[:0]
	for i := range s.fams {
		s.fams[i].ns = s.fams[i].ns[:0]
	}
	for _, n := range s.nemeses {
		if n.enabled() && (s.core == nil || n.beyond(s.core)) {
			i := s.famIndex(n.family)
			if len(s.fams[i].ns) == 0 {
				order = append(order, i)
			}
			s.fams[i].ns = append(s.fams[i].ns, n)
		}
	}
	s.famOrder = order
	if len(order) == 0 || s.core == nil && (s.settling || s.faultsOff) {
		return
	}
	var picked *nemesis
	by := nemesisOf(s.c)
	if by != nil {
		var all []*nemesis
		var names []string
		for _, i := range order {
			for _, n := range s.fams[i].ns {
				all, names = append(all, n), append(names, n.name)
			}
		}
		if k := by(names); k > 0 {
			picked = all[k-1]
		}
	}
	for _, fi := range order {
		f := &s.fams[fi]
		ns := f.ns
		k := 0
		// the logs the choice reaches: a wrapper outside one (Without, a
		// Swarm) may answer a family it turns off without asking it, and
		// that log saw no choice, nor any nemesis offered
		before := s.logLens[:0]
		eachLog(s.c, func(r *ChoiceLog) { before = append(before, logLen{r, len(r.Choices)}) })
		s.logLens = before
		if by == nil && s.core != nil {
			k = s.draw(f.kind, len(ns)+1) // a core settle: the nemeses left are outside the core
		} else if by == nil {
			s.optFam = f
			k = s.Choose(f.kind, len(ns)+1)
			s.optFam = nil
		} else if i := slices.Index(ns, picked); i >= 0 {
			k = i + 1
		}
		for _, b := range before {
			r := b.log
			if by != nil { // the script chose: record it as the choices it stands for
				r.Took(f.kind, len(ns)+1, k)
			} else if len(r.Choices) == b.n {
				continue
			}
			r.Offer(f.names())
			if k > 0 {
				r.Name(ns[k-1].name) // which nemesis, not its index among those enabled then
			}
		}
		if k > 0 {
			if s.Log != nil {
				s.Log(fmt.Sprintf("%v nemesis %s", s.Elapsed(), ns[k-1].name))
			}
			ns[k-1].do()
			return
		}
	}
}

// nemFam is a nemesis family's enabled nemeses at a step (maybeNemesis):
// its choice's kind, the nemeses, and their names, made again only when
// they change.
type nemFam struct {
	family, kind string
	ns           []*nemesis
	named        []*nemesis // the nemeses of names
	nameList     []string
}

// names are the names of f's nemeses.
func (f *nemFam) names() []string {
	if !slices.Equal(f.named, f.ns) {
		f.nameList = make([]string, len(f.ns)) // a new slice: a log may keep the last
		for i, n := range f.ns {
			f.nameList[i] = n.name
		}
		f.named = append(f.named[:0], f.ns...)
	}
	return f.nameList
}

// famIndex is family's index in s.fams, added if new.
func (s *Sim) famIndex(family string) int {
	for i := range s.fams {
		if s.fams[i].family == family {
			return i
		}
	}
	s.fams = append(s.fams, nemFam{family: family, kind: "fault.nemesis." + family})
	return len(s.fams) - 1
}

// logLen is a ChoiceLog and how many choices it held.
type logLen struct {
	log *ChoiceLog
	n   int
}

// eachLog calls f with every ChoiceLog in c's chain of wrappers.
func eachLog(c Chooser, f func(*ChoiceLog)) {
	for c != nil {
		if r, ok := c.(*ChoiceLog); ok {
			f(r)
		}
		w, ok := c.(Wrapper)
		if !ok {
			return
		}
		c = w.Unwrap()
	}
}

// A task's state, for the watchdog (Hang): the scheduler marks it running
// when it wakes it, with a generation counting its wakes, and the task
// marks itself parked when it hands control back. The watchdog abandons a
// task it sees running in one generation for Hang, with the same
// compare-and-swap, so exactly one of them sends on its back channel: the
// task's hand-back, or the watchdog's sentinel, which makes Run panic.
// An abandoned task, if it is ever let go, unwinds at its next seam
// touching nothing of the simulation's.
const (
	taskParked    = 0
	taskRunning   = 1
	taskAbandoned = 2
	taskStatus    = 3 // the status bits; the rest is the generation
)

// wake hands control to t.
func (s *Sim) wake(t *task) {
	t.wakes++
	t.state.Store(t.wakes<<2 | taskRunning)
	if s.hangOn {
		s.awaited.Store(t)
	}
	t.wake <- struct{}{}
}

// handBack marks the running task parked; false if the watchdog abandoned
// it.
func (t *task) handBack() bool {
	v := t.state.Load()
	return v&taskStatus == taskRunning && t.state.CompareAndSwap(v, v&^taskStatus|taskParked)
}

// await waits for the task t, just woken, to hand control back at a seam
// or by ending, or for the watchdog's sentinel.
func (s *Sim) await(t *task) {
	<-t.back
	if t.state.Load()&taskStatus == taskAbandoned {
		s.hung = t
		panic(s.hangMsg)
	}
}

// watch starts Hang's watchdog for one Run, and returns its stop. It looks
// a few times per Hang (a timer reset at every step cost 40% of a bare
// step, a select on a channel of the watchdog's 15%) and abandons the task
// the scheduler waits for if that task has been running, in one wake,
// since it looked Hang ago.
func (s *Sim) watch() (stop func()) {
	if s.Hang <= 0 {
		s.hangOn = false
		return func() {}
	}
	s.hangOn = true
	done := make(chan struct{})
	every := max(s.Hang/4, time.Millisecond)
	go func() { //qenv:ok the hang watchdog: it watches the scheduler in wall time, from outside the simulation
		tick := time.NewTicker(every) //qenv:ok as above
		defer tick.Stop()
		var seen int64
		var since time.Time
		for {
			select { //qenv:ok as above
			case <-done:
				return
			case now := <-tick.C:
				t := s.awaited.Load()
				if t == nil {
					continue
				}
				v := t.state.Load()
				if v&taskStatus != taskRunning || v != seen {
					seen, since = v, now
					continue
				}
				if now.Sub(since) < s.Hang {
					continue
				}
				stack := goroutineStack(t.gid.Load()) // while it is blocked where it hangs
				if !t.state.CompareAndSwap(v, v&^taskStatus|taskAbandoned) {
					continue // it handed back meanwhile
				}
				s.hangMsg = fmt.Sprintf("qenv: task %s ran %v of wall time without reaching a seam: blocked outside the simulation "+
					"(a channel, a lock, database/sql's pool at SetMaxOpenConns); raise the limit (Sim.Hang), weave it (qenv/weave), "+
					"or run under qgosim\n\n%s", t.name, s.Hang, stack)
				t.back <- struct{}{} // the sentinel: the task will never send now
				return
			}
		}
	}()
	return func() {
		close(done)
		s.awaited.Store(nil)
	}
}

// goroutineStack is the stack of goroutine id, from a dump of all of them
// (Go has no call for one other goroutine's).
func goroutineStack(id int64) string {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	head := fmt.Sprintf("goroutine %d [", id)
	for g := range strings.SplitSeq(string(buf), "\n\n") {
		if strings.HasPrefix(g, head) {
			return g
		}
	}
	return fmt.Sprintf("(goroutine %d not found)", id)
}

// notePanic records a task's panic for Run to re-raise, and its stack.
func (s *Sim) notePanic(r any, stack []byte) {
	s.panicked, s.stack = r, stack
	if dst, ok := hooks.PanicStackOf(s.base); ok { // a run asked for it (qtest's explorations)
		*dst = hooks.TaskPanic{Value: r, Stack: string(stack)}
	}
}

// PanicStack is the stack of the last task that panicked.
func (s *Sim) PanicStack() string { return string(s.stack) }

// Close unwinds the tasks still parked (their goroutines exit). Call it
// when the run is over; the simulation cannot run afterwards.
func (s *Sim) Close() {
	if s.closed {
		return
	}
	s.closed = true
	defer forget(s)
	defer disown(s.creator, s)
	defer live.Add(-1)
	if s.nat != nil {
		s.closeNative()
		return
	}
	for _, t := range s.tasks {
		if t != s.hung && !t.done { // the hung task is abandoned: let go, it unwinds alone, touching nothing
			t.killed = true
			s.wake(t)
			<-t.back
		}
	}
	s.panicked = nil
}

// Tasks lists the tasks that have not finished, with their state, for
// debugging a run that did not quiesce.
func (s *Sim) Tasks() string {
	var lines []string
	for _, t := range s.tasks {
		if t.done || t.killed {
			continue
		}
		st := "runnable"
		if t.waiting {
			st = "waiting"
		}
		if !t.deadline.IsZero() {
			st += " until " + t.deadline.Sub(s.start).String()
		}
		lines = append(lines, t.name+": "+st)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// claim registers a double's fault-kind prefix ("rpc.payments"): two
// doubles of one kind and name in a simulation would share their fault
// kinds, so a rate, a script or a report naming one would mean both.
func (s *Sim) claim(prefix, what, name string) {
	if s.doubles == nil {
		s.doubles = map[string]bool{}
	}
	if s.doubles[prefix] {
		panic(fmt.Sprintf("qenv: a second %s named %q in this simulation: its faults would be the first's (%s.*), so a rate, "+
			"a script or a report naming one would mean both; give each %s its own name", what, name, prefix, what))
	}
	s.doubles[prefix] = true
}
