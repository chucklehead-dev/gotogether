// Package woven is the ABI of woven builds: the functions and types that
// code rewritten at build time calls, and nothing a user writes. Three
// weavings call it:
//   - qenv/weave's aspects (Orchestrion's orchestrion.yml, or qweave):
//     sync.Mutex, sync.RWMutex and sync.WaitGroup method calls of the
//     root module become MutexLock, RLock, Add and the others, and a go
//     statement marked //qenv:go becomes go Go(fn)();
//   - qweave -cover: a call of Hit at the start of every block, and each
//     file's blocks registered at init (RegisterBlocks), read by qcover;
//   - qweave -mutate: every mutant behind Cond, Pick or MutantOn, and each
//     file's mutants registered at init (RegisterMutants), read by qmut.
//
// There is no compatibility promise to users: nothing here is to be
// called by hand, and any of it may change in any release. It is stable
// only within one ABI version (ABI1), for code woven by aspects and a
// qweave of that version: qweave is a module of its own, built at its own
// commit, so the code it writes names the ABI it was written for
// (var _ = woven.ABI1) and qweave refuses a module whose core lacks it. A user names qenv/weave in a tool file
// (its aspects), and reads coverage and mutants through qcover, qmut and
// qtest.
//
// Each sync function checks whether the calling goroutine is a task of a
// running simulation. If it is, the lock or wait group is the
// simulation's, keyed by the real object: taking it is a scheduling point
// and blocking on it is a simulated wait, with sync's semantics. If not
// (or if the program links no qenv), the real method runs, at the cost
// of a goroutine lookup per call: weave test builds, not production
// ones. Inside a simulation, every user of a woven object must be one of
// its tasks: a lock taken in a task and released by the goroutine that
// created the simulation would be released on the real mutex, which was
// never taken.
//
// The package imports nothing of the core but qrun and its own state
// (internal/wovenrt), so that any other package of the core can be woven
// without an import cycle.
package woven

import (
	"sync"

	"github.com/chucklehead-dev/gotogether/internal/wovenrt"
)

// ABI1 names the woven ABI this version provides, and its number. Code
// qweave writes references it, so a build against a core of another ABI
// fails to compile naming it, and qweave checks the module's core for it
// before weaving. Any change to this package's functions or their meaning
// renames it (ABI2), so old woven code stops compiling instead of calling
// a function whose meaning changed.
const ABI1 = 1

// Go is a go statement marked //qenv:go: in a simulation, fn runs as a new
// task and the go statement starts a goroutine that does nothing;
// outside, fn runs on that goroutine as written. It is evaluated by the
// goroutine executing the go statement, before the new one starts, which
// is what lets it find the simulation.
func Go(fn func()) func() {
	if ctx, ok := wovenrt.Current(); ok {
		wovenrt.Go(ctx, fn)
		return func() {}
	}
	return fn
}

// MutexLock is (*sync.Mutex).Lock.
func MutexLock(m *sync.Mutex) {
	if ctx, ok := wovenrt.Current(); ok {
		wovenrt.Lock(ctx, m)
		return
	}
	m.Lock()
}

// MutexUnlock is (*sync.Mutex).Unlock.
func MutexUnlock(m *sync.Mutex) {
	if ctx, ok := wovenrt.Current(); ok {
		wovenrt.Unlock(ctx, m)
		return
	}
	m.Unlock()
}

// MutexTryLock is (*sync.Mutex).TryLock.
func MutexTryLock(m *sync.Mutex) bool {
	if ctx, ok := wovenrt.Current(); ok {
		return wovenrt.TryLock(ctx, m)
	}
	return m.TryLock()
}

// RWLock is (*sync.RWMutex).Lock.
func RWLock(m *sync.RWMutex) {
	if ctx, ok := wovenrt.Current(); ok {
		wovenrt.Lock(ctx, m)
		return
	}
	m.Lock()
}

// RWUnlock is (*sync.RWMutex).Unlock.
func RWUnlock(m *sync.RWMutex) {
	if ctx, ok := wovenrt.Current(); ok {
		wovenrt.Unlock(ctx, m)
		return
	}
	m.Unlock()
}

// RWTryLock is (*sync.RWMutex).TryLock.
func RWTryLock(m *sync.RWMutex) bool {
	if ctx, ok := wovenrt.Current(); ok {
		return wovenrt.TryLock(ctx, m)
	}
	return m.TryLock()
}

// RLock is (*sync.RWMutex).RLock.
func RLock(m *sync.RWMutex) {
	if ctx, ok := wovenrt.Current(); ok {
		wovenrt.RLock(ctx, m)
		return
	}
	m.RLock()
}

// RUnlock is (*sync.RWMutex).RUnlock.
func RUnlock(m *sync.RWMutex) {
	if ctx, ok := wovenrt.Current(); ok {
		wovenrt.RUnlock(ctx, m)
		return
	}
	m.RUnlock()
}

// TryRLock is (*sync.RWMutex).TryRLock.
func TryRLock(m *sync.RWMutex) bool {
	if ctx, ok := wovenrt.Current(); ok {
		return wovenrt.TryRLock(ctx, m)
	}
	return m.TryRLock()
}

// Add is (*sync.WaitGroup).Add.
func Add(wg *sync.WaitGroup, n int) {
	if ctx, ok := wovenrt.Current(); ok {
		wovenrt.Add(ctx, wg, n)
		return
	}
	wg.Add(n)
}

// Done is (*sync.WaitGroup).Done.
func Done(wg *sync.WaitGroup) { Add(wg, -1) }

// Wait is (*sync.WaitGroup).Wait.
func Wait(wg *sync.WaitGroup) {
	if ctx, ok := wovenrt.Current(); ok {
		wovenrt.Wait(ctx, wg)
		return
	}
	wg.Wait()
}

// GroupGo is (*sync.WaitGroup).Go: f runs in a new task (or goroutine),
// counted by wg.
func GroupGo(wg *sync.WaitGroup, f func()) {
	Add(wg, 1)
	run := Go(func() {
		defer Done(wg)
		f()
	})
	go run() //qenv:ok an empty goroutine in a simulation (Go started the task); the real one outside
}
