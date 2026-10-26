package qtrace

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// with is s changed by f, on a copy.
func with(s Step, f func(*Step)) Step {
	f(&s)
	return s
}

// Every single mutation of a small log, written out by hand, is what
// Mutations makes, in its order: two recorders, two actors, an int and
// a string argument and a bool observation.
func TestMutationsAgainstBruteForce(t *testing.T) {
	t.Parallel()
	t0 := time.Unix(1000, 0)
	ms := time.Millisecond
	s0 := Step{Action: "inc", Seq: 1, Recorder: "a", Actor: "A", Process: "a", Thread: "a", Time: t0, Args: map[string]any{"n": int64(1)}}
	s1 := Step{Action: "put", Seq: 1, Recorder: "b", Actor: "B", Process: "b", Thread: "b", Time: t0.Add(5 * ms), Args: map[string]any{"k": "x"}}
	s2 := Step{Action: "get", Seq: 2, Recorder: "a", Actor: "A", Process: "a", Thread: "a", Time: t0.Add(10 * ms), Obs: map[string]any{"v": true}}
	log := []Step{s0, s1, s2}
	seq := func(s Step, n uint64) Step { return with(s, func(s *Step) { s.Seq = n }) }
	arg := func(s Step, k string, v any) Step {
		return with(s, func(s *Step) {
			s.Args = map[string]any{}
			if v != nil {
				s.Args[k] = v
			}
		})
	}
	at := func(s Step, d time.Duration) Step { return with(s, func(s *Step) { s.Time = t0.Add(d) }) }
	actor := func(s Step, a, p string) Step { return with(s, func(s *Step) { s.Actor, s.Process = a, p }) }
	type m struct {
		op            string
		index         int
		field, change string
		steps         []Step
	}
	want := []m{
		{"drop", 0, "", "", []Step{s1, seq(s2, 1)}},
		{"drop", 1, "", "", []Step{s0, s2}},
		{"drop", 2, "", "", []Step{s0, s1}},
		{"dup", 0, "", "", []Step{s0, seq(s0, 2), s1, seq(s2, 3)}},
		{"dup", 1, "", "", []Step{s0, s1, seq(s1, 2), s2}},
		{"dup", 2, "", "", []Step{s0, s1, s2, seq(s2, 3)}},
		{"swap", 0, "", "", []Step{at(seq(s2, 1), 0), s1, at(seq(s0, 2), 10*ms)}},
		{"corrupt", 0, "n", "+1", []Step{arg(s0, "n", int64(2)), s1, s2}},
		{"corrupt", 0, "n", "-1", []Step{arg(s0, "n", int64(0)), s1, s2}},
		{"corrupt", 0, "n", "remove", []Step{arg(s0, "n", nil), s1, s2}},
		{"corrupt", 1, "k", "empty", []Step{s0, arg(s1, "k", ""), s2}},
		{"corrupt", 1, "k", "change", []Step{s0, arg(s1, "k", "x~"), s2}},
		{"corrupt", 1, "k", "remove", []Step{s0, arg(s1, "k", nil), s2}},
		{"corrupt", 2, "obs.v", "flip", []Step{s0, s1, with(s2, func(s *Step) { s.Obs = map[string]any{"v": false} })}},
		{"corrupt", 2, "obs.v", "remove", []Step{s0, s1, with(s2, func(s *Step) { s.Obs = map[string]any{} })}},
		// past put at 5ms, to the middle of put and get; put, past get,
		// the last, as far after it as it was before
		{"retime", 0, "", "", []Step{s1, at(s0, 7500*time.Microsecond), s2}},
		{"retime", 1, "", "", []Step{s0, s2, at(s1, 15*ms)}},
		// B has no step at or before t0: its first's process
		{"reactor", 0, "", "", []Step{actor(s0, "B", "b"), s1, s2}},
		{"reactor", 1, "", "", []Step{s0, actor(s1, "A", "a"), s2}},
		{"reactor", 2, "", "", []Step{s0, s1, actor(s2, "B", "b")}},
	}
	got := Mutations(log)
	if len(got) != len(want) {
		var ops []string
		for _, g := range got {
			ops = append(ops, fmt.Sprintf("%s %d %s%s", g.Op, g.Index, g.Field, g.Change))
		}
		t.Fatalf("%d mutants, want %d: %s", len(got), len(want), strings.Join(ops, "; "))
	}
	for i, w := range want {
		g := got[i]
		if g.Op != w.op || g.Index != w.index || g.Field != w.field || g.Change != w.change || g.Action != log[w.index].Action {
			t.Errorf("mutant %d: %s %d %q %q (%s), want %s %d %q %q", i, g.Op, g.Index, g.Field, g.Change, g.Action, w.op, w.index, w.field, w.change)
			continue
		}
		if !reflect.DeepEqual(g.Steps, w.steps) {
			t.Errorf("mutant %d (%s):\n got %v\nwant %v", i, g, g.Steps, w.steps)
		}
		if g.Detail == "" {
			t.Errorf("mutant %d (%s %d): no detail", i, g.Op, g.Index)
		}
	}
	// the original is untouched
	if !reflect.DeepEqual(log, []Step{s0, s1, s2}) || s0.Args["n"] != int64(1) || s2.Obs["v"] != true {
		t.Errorf("Mutations changed its input: %v", log)
	}
}

// contractLog is a log with what a recorder's contract is about:
// invocation records and their completions, a settle record, a lost
// suffix, two actors, two incarnations.
func contractLog() []Step {
	t0 := time.Unix(2000, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	return []Step{
		{Action: "put", Seq: 1, Recorder: "c", Actor: "kv", Process: "c", Thread: "c", Time: at(0), Invocation: true, Args: map[string]any{"k": "x", "v": int64(1)}},
		{Action: "put", Seq: 2, Recorder: "c", Actor: "kv", Process: "c", Thread: "c", Time: at(3), Call: 1, Invoked: at(0), Args: map[string]any{"k": "x", "v": int64(1)}, Outcome: "ok"},
		{Action: "get", Seq: 3, Recorder: "c", Actor: "kv", Process: "c", Thread: "c", Time: at(4), Invocation: true, Args: map[string]any{"k": "x"}},
		{Action: "get", Seq: 4, Recorder: "c", Actor: "kv", Process: "c", Thread: "c", Time: at(7), Call: 3, Invoked: at(4), Args: map[string]any{"k": "x"}, Obs: map[string]any{"v": int64(1), "found": true}},
		{Action: "apply", Seq: 1, Recorder: "s", Actor: "kv", Process: "c", Thread: "s", Time: at(2), Args: map[string]any{"k": "x"}},
		{Action: "tick", Seq: 2, Recorder: "s", Actor: "clock", Process: "s1", Thread: "s", Time: at(5)},
		{Action: "tick", Seq: 3, Recorder: "s", Actor: "clock", Process: "s2", Thread: "s", Time: at(6), Order: map[string]int64{"lamport": 4}},
		{Action: "drained", Seq: 4, Recorder: "s", Actor: "clock", Process: "s2", Thread: "s", Time: at(8)},
		{Action: "settle", Seq: 5, Recorder: "s", Actor: "clock", Process: "s2", Thread: "s", Time: at(9), Settled: true},
		{Action: "lost", Recorder: "c", Time: at(10), Lost: "suffix", LostAfter: 4},
	}
}

// reconstructErrors are the error issues Reconstruct reports of steps,
// and its gap, duplicate and invocation issues of any severity: what a
// recorder that breaks its contract shows.
func reconstructErrors(steps []Step) []string {
	traces, issues := Reconstruct(steps)
	for _, tr := range traces {
		issues = append(issues, tr.Issues...)
	}
	var out []string
	for _, is := range issues {
		if is.Severity == "error" || is.Kind == "gap" || is.Kind == "duplicate" || is.Kind == "invocation" {
			out = append(out, is.String())
		}
	}
	slices.Sort(out)
	return out
}

// No mutant breaks the recorder's contract: every one is judged by the
// model and the binding, not stopped at a gap, a duplicate, or a
// completion before its invocation. And each op changes what it says:
// a drop one model step fewer, a dup one more, a corruption one field.
func TestMutationsKeepTheRecorderContract(t *testing.T) {
	t.Parallel()
	log := contractLog()
	base := reconstructErrors(log)
	if len(base) > 0 {
		t.Fatalf("the log itself: %v", base)
	}
	models := func(steps []Step) int {
		n := 0
		for i := range steps {
			if steps[i].Model() {
				n++
			}
		}
		return n
	}
	byOp := map[string]int{}
	for _, m := range Mutations(log) {
		byOp[m.Op]++
		if errs := reconstructErrors(m.Steps); len(errs) > 0 {
			t.Errorf("%s: %v", m, errs)
		}
		switch m.Op {
		case "drop":
			if models(m.Steps) != models(log)-1 {
				t.Errorf("%s: %d model steps", m, models(m.Steps))
			}
		case "dup":
			if models(m.Steps) != models(log)+1 {
				t.Errorf("%s: %d model steps", m, models(m.Steps))
			}
		case "corrupt":
			if len(m.Steps) != len(log) {
				t.Fatalf("%s: %d steps", m, len(m.Steps))
			}
			diff := 0
			for i := range log {
				if !reflect.DeepEqual(log[i], m.Steps[i]) {
					diff++
				}
			}
			if diff != 1 {
				t.Errorf("%s: %d steps differ", m, diff)
			}
		}
		// a recorder's loss and its completions name the seqs they did
		for i := range m.Steps {
			s := &m.Steps[i]
			if s.Lost != "" && s.Recorder == "c" {
				last := uint64(0)
				for _, o := range m.Steps {
					if o.Recorder == "c" && o.Lost == "" {
						last = max(last, o.Seq)
					}
				}
				if s.LostAfter != last {
					t.Errorf("%s: c's suffix lost after %d, its last seq %d", m, s.LostAfter, last)
				}
			}
		}
	}
	// 6 model steps (two of them completions) out of 10 records; the swaps
	// of adjacent model steps of one recorder and incarnation (c's are
	// split by invocation records; s's: the last tick and drained, not
	// apply, in kv's process, and a tick, nor the ticks of two
	// incarnations); 7 fields corrupted three ways or
	// two, removal included; every step retimed but drained, the last
	// in time
	want := map[string]int{"drop": 6, "dup": 6, "swap": 1, "corrupt": 17, "retime": 5, "reactor": 6}
	if !reflect.DeepEqual(byOp, want) {
		t.Errorf("mutants by op %v, want %v", byOp, want)
	}
}

// MutateOps and MutateLimit narrow the mutants: only the ops named, and
// at most n of each, spread over the log from its first.
func TestMutationsNarrowed(t *testing.T) {
	t.Parallel()
	log := contractLog()
	all := Mutations(log)
	got := Mutations(log, MutateOps("corrupt", "drop"), MutateLimit(3))
	var ops []string
	for _, m := range got {
		ops = append(ops, fmt.Sprintf("%s %d", m.Op, m.Index))
	}
	// of each op's, the k*len/3-th
	var drops, corrupts []StepMutant
	for _, m := range all {
		switch m.Op {
		case "drop":
			drops = append(drops, m)
		case "corrupt":
			corrupts = append(corrupts, m)
		}
	}
	want := []string{
		fmt.Sprintf("drop %d", drops[0].Index), fmt.Sprintf("drop %d", drops[2].Index), fmt.Sprintf("drop %d", drops[4].Index),
		fmt.Sprintf("corrupt %d", corrupts[0].Index), fmt.Sprintf("corrupt %d", corrupts[5].Index), fmt.Sprintf("corrupt %d", corrupts[11].Index),
	}
	if !slices.Equal(ops, want) {
		t.Errorf("narrowed: %v, want %v", ops, want)
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "the ops are drop, dup, swap, corrupt, retime, reactor") {
			t.Errorf("an unknown op: %v", r)
		}
	}()
	MutateOps("delete")
}

// Two steps alike in everything but their place (a poll that saw the
// same thing twice) are not swapped: the swap would be the log itself.
// A step of another action between them is, with each.
func TestMutationsSwapNoAlike(t *testing.T) {
	t.Parallel()
	t0 := time.Unix(3000, 0)
	poll := func(seq uint64) Step {
		return Step{Action: "poll", Seq: seq, Recorder: "p", Actor: "a", Process: "p", Thread: "p", Time: t0.Add(time.Duration(seq) * time.Millisecond), Obs: map[string]any{"status": "running"}}
	}
	if ms := Mutations([]Step{poll(1), poll(2)}, MutateOps("swap")); len(ms) != 0 {
		t.Errorf("alike steps swapped: %v", ms)
	}
	mid := poll(2)
	mid.Action = "cancel"
	if ms := Mutations([]Step{poll(1), mid, poll(3)}, MutateOps("swap")); len(ms) != 2 {
		t.Errorf("%d swaps around a cancel, want 2: %v", len(ms), ms)
	}
}
