package qnemesis

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// WatchHost's probes record a stall where their wait was held past the
// threshold: a tick held 300ms is a sched stall about that long, ending
// when the tick ran; an fsync held 300ms a disk stall over the fsync. The
// stalls are measured in the harness, apart from any system. Stop removes
// the disk probe's file, and a second Stop returns the same report. The
// threshold sits just under the tolerance below hold: a real stall of the
// runner (a 141ms fsync on a loaded -race job) is no stall here, and every
// one recorded is a held probe's.
func TestWatchHostMeasuresStalls(t *testing.T) {
	t.Parallel()
	const hold = 300 * time.Millisecond
	var ticks, syncs atomic.Int32
	sleep := func(d time.Duration) {
		if ticks.Add(1) == 5 { // both probes sleep here: one of them holds once
			time.Sleep(hold)
		}
		time.Sleep(d)
	}
	sync := func(f *os.File) error {
		if syncs.Add(1) == 3 {
			time.Sleep(hold)
		}
		return f.Sync()
	}
	dir := t.TempDir()
	m, err := watchHost(HostWatch{Dir: dir, Threshold: hold - 50*time.Millisecond}, sleep, sync)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	time.Sleep(time.Second)
	rep := m.Stop()
	after := time.Now()
	kinds := map[string]int{}
	for _, s := range rep.Stalls {
		kinds[s.Kind]++
		if d := s.Duration(); d < hold-50*time.Millisecond || d > 3*hold {
			t.Errorf("%s: measured %s, held %s", s, d, hold)
		}
		if s.Start.Before(before.Add(-time.Second)) || s.End.After(after) {
			t.Errorf("%s: outside the watch [%s, %s]", s, before, after)
		}
	}
	// the held sleep is a tick's or the disk probe's (which then fsyncs
	// late, not long): a disk stall from the held fsync either way
	if kinds["disk"] < 1 || len(rep.Stalls) < 1 || rep.Ticks < 10 || rep.Syncs < 5 {
		t.Errorf("want a disk stall and the probes run: %s", rep)
	}
	if es, _ := filepath.Glob(filepath.Join(dir, ".qnemesis-probe-*")); len(es) > 0 {
		t.Errorf("the disk probe's file left: %v", es)
	}
	if again := m.Stop(); again.Ticks != rep.Ticks || len(again.Stalls) != len(rep.Stalls) {
		t.Errorf("a second Stop: %s, the first %s", again, rep)
	}
}

// A tick held past the threshold is a sched stall: the ticker alone.
func TestWatchHostSchedStall(t *testing.T) {
	t.Parallel()
	var n atomic.Int32
	m, err := watchHost(HostWatch{Threshold: 150 * time.Millisecond}, func(d time.Duration) {
		if n.Add(1) == 3 {
			time.Sleep(400 * time.Millisecond)
		}
		time.Sleep(d)
	}, (*os.File).Sync)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	rep := m.Stop()
	found := false
	for _, s := range rep.Stalls {
		found = found || s.Kind == "sched" && s.Duration() >= 350*time.Millisecond
	}
	if !found || rep.MaxTick < 350*time.Millisecond {
		t.Errorf("want a sched stall of about 400ms: %s", rep)
	}
}
