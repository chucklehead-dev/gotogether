package qnemesis

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

// HostWatch configures WatchHost: how often its probes run and how long
// a delay must be to be a stall.
type HostWatch struct {
	// Every is the scheduling probe's tick (default 10ms): a tick that
	// comes late by more than Threshold is a stall of the host's CPU (the
	// harness not scheduled: a busy or throttled machine, a paused VM).
	Every time.Duration
	// Threshold is the shortest delay recorded as a stall (default
	// 100ms). Choose it against the bounds the runs are judged by: a
	// stall that could by itself use up a meaningful share of one, well
	// above the probes' own jitter (a few ms on a loaded machine).
	Threshold time.Duration
	// Dir, if set, is where the disk probe writes and fsyncs a few bytes
	// every SyncEvery (default 20ms): give it a directory on the system's
	// own disk (its write-ahead logs'), so a stall of that disk, which
	// every node's fsync shares and no ticker sees, is measured. An fsync
	// that takes longer than Threshold is a stall.
	Dir       string
	SyncEvery time.Duration
}

// A HostReport is what WatchHost measured: the stalls, and what the
// probes saw short of one (their longest delays), which says how quiet
// the host was.
type HostReport struct {
	Stalls    []qtrace.Stall
	Threshold time.Duration
	// Ticks and Syncs are how often each probe ran; MaxTick is the
	// longest a tick came late, MaxSync the longest fsync.
	Ticks, Syncs     int
	MaxTick, MaxSync time.Duration
}

// String says what was measured, in a line.
func (r HostReport) String() string {
	s := fmt.Sprintf("%d stalls over %s (%d ticks, the latest %s late", len(r.Stalls), r.Threshold, r.Ticks, r.MaxTick.Round(time.Millisecond))
	if r.Syncs > 0 {
		s += fmt.Sprintf("; %d fsyncs, the longest %s", r.Syncs, r.MaxSync.Round(100*time.Microsecond))
	}
	s += ")"
	for _, st := range r.Stalls {
		s += "; " + st.String()
	}
	return s
}

// A HostMonitor measures the host's stalls from the harness, outside the
// system under test (WatchHost); Stop ends it.
type HostMonitor struct {
	w     HostWatch
	stop  chan struct{}
	wg    sync.WaitGroup
	mu    sync.Mutex
	rep   HostReport
	probe string // the disk probe's file
	// sleep and sync are the probes' waits, a test's to stall
	sleep func(time.Duration)
	sync  func(*os.File) error
	once  sync.Once
}

// WatchHost starts measuring the host's stalls: a ticker on a thread of
// its own (on Linux also reading that thread's scheduling delay and the
// cgroup's CPU throttling, which say why it was late) and, with Dir, an
// fsync on the system's disk. Run it in the harness, beside the system,
// for as long as the system runs, then Stop it and give the stalls to
// the check (qtest.HostStalls): a bounded response a stall could have
// made late by itself is no verdict. A slow system does not stall the
// probes, which share only the host with it; a system that saturates the
// host's CPU or disk itself would, which the stall's length against the
// lateness it must explain (validate.Report.ExcuseStalls) does not rule
// out.
func WatchHost(w HostWatch) (*HostMonitor, error) {
	return watchHost(w, time.Sleep, (*os.File).Sync)
}

func watchHost(w HostWatch, sleep func(time.Duration), syncf func(*os.File) error) (*HostMonitor, error) {
	if w.Every <= 0 {
		w.Every = 10 * time.Millisecond
	}
	if w.Threshold <= 0 {
		w.Threshold = 100 * time.Millisecond
	}
	if w.SyncEvery <= 0 {
		w.SyncEvery = 20 * time.Millisecond
	}
	m := &HostMonitor{w: w, stop: make(chan struct{}), rep: HostReport{Threshold: w.Threshold}, sleep: sleep, sync: syncf}
	var f *os.File
	if w.Dir != "" {
		var err error
		if f, err = os.CreateTemp(w.Dir, ".qnemesis-probe-*"); err != nil {
			return nil, fmt.Errorf("qnemesis: the disk probe: %w", err)
		}
		m.probe = f.Name()
	}
	m.wg.Go(m.tick)
	if f != nil {
		m.wg.Go(func() { m.fsync(f) })
	}
	return m, nil
}

// Stop ends the probes and returns what they measured. It may be called
// again: the same report.
func (m *HostMonitor) Stop() HostReport {
	m.once.Do(func() {
		close(m.stop)
		m.wg.Wait()
		if m.probe != "" {
			_ = os.Remove(m.probe)
		}
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rep
	r.Stalls = append([]qtrace.Stall(nil), m.rep.Stalls...)
	return r
}

func (m *HostMonitor) stopped() bool {
	select {
	case <-m.stop:
		return true
	default:
		return false
	}
}

// tick is the scheduling probe: a tick due every Every, a late one a
// stall from when it was due to when it ran (the stall lies within the
// tick's window; placed at its end, it is never longer than measured).
func (m *HostMonitor) tick() {
	runtime.LockOSThread() // one thread, whose scheduling delay is read
	defer runtime.UnlockOSThread()
	cpu := newCPUStats()
	last := time.Now()
	for !m.stopped() {
		m.sleep(m.w.Every)
		now := time.Now()
		late := now.Sub(last) - m.w.Every
		waited, throttled := cpu.delta()
		m.mu.Lock()
		m.rep.Ticks++
		m.rep.MaxTick = max(m.rep.MaxTick, late)
		if late > m.w.Threshold {
			m.rep.Stalls = append(m.rep.Stalls, qtrace.Stall{Start: last.Add(m.w.Every).Round(0), End: now.Round(0), Kind: "sched", Detail: cpu.say(waited, throttled)})
		}
		m.mu.Unlock()
		last = now
	}
}

// fsync is the disk probe: a few bytes written over and fsynced every
// SyncEvery, an fsync that takes longer than Threshold a stall.
func (m *HostMonitor) fsync(f *os.File) {
	defer f.Close()
	buf := []byte("qnemesis")
	for !m.stopped() {
		m.sleep(m.w.SyncEvery)
		start := time.Now()
		_, err := f.WriteAt(buf, 0)
		if err == nil {
			err = m.sync(f)
		}
		end := time.Now()
		took := end.Sub(start)
		m.mu.Lock()
		m.rep.Syncs++
		m.rep.MaxSync = max(m.rep.MaxSync, took)
		if took > m.w.Threshold {
			detail := fmt.Sprintf("a write and fsync in %s took %s", filepath.Dir(f.Name()), took.Round(time.Millisecond))
			if err != nil {
				detail += ", failing: " + err.Error()
			}
			m.rep.Stalls = append(m.rep.Stalls, qtrace.Stall{Start: start.Round(0), End: end.Round(0), Kind: "disk", Detail: detail})
		}
		m.mu.Unlock()
	}
}

// say is what the CPU's counters say of a late tick: the time its thread
// waited for a CPU and the cgroup was throttled, when they can be read.
func (c *cpuStats) say(waited, throttled time.Duration) string {
	switch {
	case !c.sched && !c.cgroup:
		return ""
	case c.sched && c.cgroup:
		return fmt.Sprintf("the probe's thread waited %s for a CPU; the cgroup was throttled %s", waited.Round(time.Millisecond), throttled.Round(time.Millisecond))
	case c.sched:
		return fmt.Sprintf("the probe's thread waited %s for a CPU", waited.Round(time.Millisecond))
	}
	return fmt.Sprintf("the cgroup was throttled %s", throttled.Round(time.Millisecond))
}
