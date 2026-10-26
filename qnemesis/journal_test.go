package qnemesis

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// A real run's journal is a qenv Script that makes the same faults, in
// the order they happened, each after the steps before it, and the
// script runs to its end in a simulation of the same processes: a failed
// real run's faults re-made where they can be explored.
func TestJournalRoundTrip(t *testing.T) {
	t.Parallel()
	n, _, _ := fakes()
	var k atomic.Int64
	n.Steps = func() int { return int(k.Add(1)) } // the system makes progress between looks
	var journal bytes.Buffer
	n.Journal = &journal
	s := Schedule{
		{Kind: "nemesis.crash.a", After: 2},
		{Kind: "nemesis.partition.b.a", After: 4, For: 5 * time.Millisecond},
		{Kind: "nemesis.isolate.c", Follows: []int{1}},
		{Kind: "nemesis.heal.c", Follows: []int{2}},
		{Kind: "nemesis.pause.b", Follows: []int{3}, For: 5 * time.Millisecond},
		{Kind: "nemesis.latency.a.b", Arg: 5 * time.Millisecond, Follows: []int{4}, For: 5 * time.Millisecond},
	}
	if err := n.Run(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	j, err := ReadJournal(strings.NewReader(journal.String() + `{"index":9,"kind":"nemesis.cr`)) // a torn last line
	if err != nil {
		t.Fatal(err)
	}
	sc := j.Script()
	var kinds []string
	for i, f := range sc.Faults {
		kinds = append(kinds, f.Kind)
		if i > 0 && !reflect.DeepEqual(f.Follows, []int{i - 1}) {
			t.Errorf("scripted %d (%v) follows %v; want the one before", i, f, f.Follows)
		}
	}
	want := []string{"nemesis.crash.a", "nemesis.partition.a.b", "nemesis.heal.a.b", "nemesis.partition.a.c", "nemesis.partition.b.c",
		"nemesis.heal.a.c", "nemesis.heal.b.c", "nemesis.pause.b"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("script %v;\nwant %v (from %+v)", kinds, want, j.Entries)
	}
	// After: the system's steps then, and the faults recorded before (a
	// simulation recording its faults counts them)
	recorded := 0
	var after []int
	for _, e := range j.Entries {
		if fam, _, _ := parse(e.Kind); fam != "restart" && fam != "resume" && fam != "clear" && fam != "latency" {
			for range max(1, len(e.Links)) {
				after = append(after, *e.Steps+recorded)
			}
		}
		if e.Recorded {
			recorded++
		}
	}
	for i, f := range sc.Faults {
		if f.After != after[i] {
			t.Errorf("scripted %d (%s) after %d steps; want %d", i, f.Kind, f.After, after[i])
		}
	}
	if sc.Faults[0].After < 2 || sc.Faults[1].After < 5 {
		t.Errorf("the faults' After below the schedule's: %v", sc.Faults)
	}
	if d := j.Dropped(); len(d) != 1 || d[0].Kind != "nemesis.latency.a.b" {
		t.Errorf("dropped %v; want the latency", d)
	}

	// the script in a simulation of a, b and c: every fault made
	rec := qobs.NewRecorder(&qobs.MemorySink{})
	sc.Progress = rec.Count
	sim := qenv.New(sc)
	defer sim.Close()
	sim.Stalls = qenv.Stalls{Pause: 5 * time.Millisecond}
	var procs []*qenv.Process
	for _, name := range []string{"a", "b", "c"} {
		procs = append(procs, sim.Process(name, true, func(ctx context.Context) {
			qenv.Daemon(ctx)
			for {
				rec.Record(ctx, "tick", nil, "node", name)
				qenv.Sleep(ctx, time.Millisecond)
			}
		}))
	}
	sim.Net().PartitionNemesis(procs...)
	if err := sim.Run(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if m := sc.Misnamed(); len(m) > 0 {
		t.Fatalf("the journal's script names faults the simulation lacks: %v", m)
	}
	if r := sc.Remaining(); len(r) > 0 {
		t.Fatalf("the simulation never made %v", r)
	}
}

// FromScript keeps a Script's kinds, steps and order.
func TestFromScript(t *testing.T) {
	t.Parallel()
	sc := &qenv.Script{Faults: []qenv.Scripted{
		{Kind: "nemesis.crash.a", After: 3},
		{Kind: "nemesis.partition.a.b", After: 5, Follows: []int{0}},
	}}
	got := FromScript(sc)
	want := Schedule{{Kind: "nemesis.crash.a", After: 3}, {Kind: "nemesis.partition.a.b", After: 5, Follows: []int{0}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FromScript: %+v; want %+v", got, want)
	}
}
