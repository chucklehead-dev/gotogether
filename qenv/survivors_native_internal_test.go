package qenv

import (
	"context"
	"runtime/debug"
	"testing"
	"testing/synctest"
	"time"
)

// A native task's goroutine is live while it runs, and is its
// simulation's until it exits: the goroutine hooks (taskOf with a context
// that carries no task, the recorder's resolver, the discard check) skip
// their lookup when nothing is live, so a task that did not count would
// find no simulation once the counter fell to zero (a task's goroutine
// unwinding after its Sim closed, on a Platform), and a task that never
// stopped counting would keep every later lookup paying. Once the task
// ends, its goroutine is no simulation's owner, only an ended task of the
// run (a goroutine it started still belongs there), and once the Sim
// closes not even that: Go never reuses goroutine ids, so a leftover entry
// is a leak that also attributes a stray goroutine's steps to a closed run.
func TestNativeTaskGoroutineIsLiveAndOwnedWhileItRuns(t *testing.T) {
	base := live.Load()
	s := NewNative(Nominal{}, nil)
	var during int64
	var id int64
	var ownedBy *task
	s.Spawn("task", func(ctx context.Context) {
		during, id = live.Load(), goid()
		if o, ok := ownerOf(id); ok {
			ownedBy = o.t
		}
	})
	if err := s.Run(10 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	after := live.Load()
	_, ownedAfter := ownerOf(id)
	owners.Lock()
	_, endedAfter := owners.ended[id]
	owners.Unlock()
	s.Close()
	closed := live.Load()
	_, ownedClosed := ownerOf(id)
	owners.Lock()
	_, endedClosed := owners.ended[id]
	owners.Unlock()
	if during != base+2 || after != base+1 || closed != base {
		t.Errorf("live: %d while the task ran, %d after it ended, %d after Close; want %d, %d, %d", during, after, closed, base+2, base+1, base)
	}
	if ownedBy == nil || ownedBy.name != "task" {
		t.Errorf("the task's goroutine is owned by %v while it runs", ownedBy)
	}
	if ownedAfter || !endedAfter {
		t.Errorf("after the task ended: its goroutine owned=%v, an ended task=%v; want false, true", ownedAfter, endedAfter)
	}
	if ownedClosed || endedClosed {
		t.Errorf("after Close: the goroutine owned=%v, an ended task=%v; want neither", ownedClosed, endedClosed)
	}
}

// fakeMachine is a cooperative Platform's machine on real goroutines (see
// survivors_native_test.go: in a synctest bubble they take turns). It
// cannot stop goroutines; it stops its main loop by closing the queue the
// incarnation's main serves, which a real machine's crash ends with it.
type fakeMachine struct {
	main     func()
	proc     *Process
	restarts int
}

type fakePlatform struct{ machines map[string]*fakeMachine }

func (f *fakePlatform) Machine(name string, main func()) Machine {
	m := &fakeMachine{main: main}
	f.machines[name] = m
	go main() //qenv:ok the fake machine's main goroutine
	return m
}

func (m *fakeMachine) Crash() { close(m.proc.queue) } // the incarnation's queue: its main ends

func (m *fakeMachine) Restart() {
	m.restarts++
	go m.main() //qenv:ok the fake machine's main goroutine, booting the new incarnation's queue
}

// On a Platform a process's tasks start on its machine, so a restart must
// restart the machine: the new incarnation's main task is queued on the
// machine and runs only once it boots again. A process crashed by another
// task and restarted from an OnCrash hook runs its main again, as
// incarnation 2.
func TestNativePlatformRestartBootsTheMachine(t *testing.T) {
	if raceBuildInternal() {
		t.Skip("a cooperative Platform runs one goroutine at a time; real goroutines under -race do not")
	}
	synctest.Test(t, func(t *testing.T) {
		plat := &fakePlatform{machines: map[string]*fakeMachine{}}
		s := NewNative(Nominal{}, plat)
		s.OnCrash(func(p *Process) { p.Restart() })
		var ran []int
		p := s.Process("node", false, func(ctx context.Context) {
			ran = append(ran, ProcessOf(ctx).Incarnation)
			Wait(ctx, func() bool { return false })
		})
		m := plat.machines["node"]
		m.proc = p
		s.Spawn("killer", func(ctx context.Context) {
			Sleep(ctx, 10*time.Millisecond)
			p.Crash()
		})
		if err := s.Run(50 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		s.Close()
		close(p.queue)               // the last incarnation's machine stops
		time.Sleep(time.Millisecond) //qenv:ok the bubble's fake clock: the tasks unwind
		if len(ran) != 2 || ran[0] != 1 || ran[1] != 2 || m.restarts != 1 {
			t.Fatalf("main ran as incarnations %v, the machine restarted %d times; want [1 2], once", ran, m.restarts)
		}
	})
}

// raceBuildInternal is raceBuild of survivors_native_test.go, which this
// package's internal tests cannot see.
func raceBuildInternal() bool {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, s := range bi.Settings {
		if s.Key == "-race" && s.Value == "true" {
			return true
		}
	}
	return false
}
