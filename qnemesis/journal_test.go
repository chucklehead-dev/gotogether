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
	// each fault follows the last before it that is no heal; a heal, the
	// one before it
	lastFault := -1
	for i, f := range sc.Faults {
		kinds = append(kinds, f.Kind)
		heal := strings.HasPrefix(f.Kind, "nemesis.heal.")
		want := lastFault
		if heal {
			want = i - 1
		}
		if i > 0 && !reflect.DeepEqual(f.Follows, []int{want}) {
			t.Errorf("scripted %d (%v) follows %v; want %d", i, f, f.Follows, want)
		}
		if !heal {
			lastFault = i
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

// A real partition longer than qenv's cuts: its re-made heal never
// happens (qenv healed the link first), and the faults after it still do,
// since none waits for a heal; Unreproducible says what to set, and with
// CutLength at LongestCut the heal happens too. A heal of a link never
// cut is left out, and said.
func TestJournalScriptLongPartition(t *testing.T) {
	t.Parallel()
	t0 := time.Unix(1000, 0)
	zero, five, six := 0, 5, 6
	j := &Journal{Entries: []Entry{
		{Index: 0, Kind: "nemesis.partition.a.b", Event: "start", Time: t0, Steps: &zero, Recorded: true},
		{Index: 0, Kind: "nemesis.heal.a.b", Event: "end", Time: t0.Add(3 * time.Second), Steps: &five, Recorded: true},
		{Index: 1, Kind: "nemesis.heal.a", Event: "start", Time: t0.Add(3 * time.Second), Steps: &five, Links: [][2]string{{"a", "b"}}},
		{Index: 2, Kind: "nemesis.crash.a", Event: "start", Time: t0.Add(4 * time.Second), Steps: &six, Recorded: true},
	}}
	if d := j.LongestCut(); d != 3*time.Second {
		t.Fatalf("LongestCut %v; want 3s", d)
	}
	un := strings.Join(j.Unreproducible(), "\n")
	for _, want := range []string{"set Sim.Net().CutLength to at least 3s", "nemesis.heal.a (fault 1) healed a.b, which no fault of the journal cut"} {
		if !strings.Contains(un, want) {
			t.Errorf("Unreproducible lacks %q:\n%s", want, un)
		}
	}
	for _, cut := range []time.Duration{0, j.LongestCut()} {
		sc := j.Script()
		if kinds := len(sc.Faults); kinds != 3 {
			t.Fatalf("script %v; want the partition, its heal and the crash", sc.Faults)
		}
		steps := 0
		sc.Progress = func() int { return steps }
		s := qenv.New(sc)
		if cut > 0 {
			s.Net().CutLength = cut
		}
		a := s.Process("a", true, func(ctx context.Context) { qenv.Daemon(ctx); qenv.Sleep(ctx, time.Hour) })
		b := s.Process("b", true, func(ctx context.Context) { qenv.Daemon(ctx); qenv.Sleep(ctx, time.Hour) })
		s.Net().PartitionNemesis(a, b)
		s.Spawn("work", func(ctx context.Context) {
			for range 12 {
				qenv.Sleep(ctx, 500*time.Millisecond) // a step every half second, as the real system took
				steps++
			}
		})
		if err := s.Run(10 * time.Second); err != nil {
			t.Fatal(err)
		}
		s.Close()
		r := sc.Remaining()
		switch {
		case cut == 0 && (len(r) != 1 || r[0].Kind != "nemesis.heal.a.b"):
			t.Errorf("default CutLength: remaining %v; want only the heal qenv made first", r)
		case cut > 0 && len(r) != 0:
			t.Errorf("CutLength %v: remaining %v; want none", cut, r)
		}
	}
}
