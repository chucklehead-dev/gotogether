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
func TestMutationsOfASmallLog(t *testing.T) {
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
		// no gap: each step is its recorder's first or last
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

// The gap op drops a step between two of its recorder's and renumbers
// nothing: the hole is the mutation, a log a sink that dropped the step
// writes, where drop's renumbered log shows no loss.
func TestGapMutants(t *testing.T) {
	t.Parallel()
	t0 := time.Unix(1000, 0)
	step := func(seq uint64, rec string) Step {
		return Step{Action: "inc", Seq: seq, Recorder: rec, Actor: "A", Process: rec, Thread: rec, Time: t0.Add(time.Duration(seq) * time.Millisecond)}
	}
	a1, a2, a3, b1 := step(1, "a"), step(2, "a"), step(3, "a"), step(1, "b")
	got := Mutations([]Step{a1, b1, a2, a3}, MutateOps("gap"))
	if len(got) != 1 || got[0].Index != 2 || !reflect.DeepEqual(got[0].Steps, []Step{a1, b1, a3}) {
		t.Fatalf("gap mutants %v, want a2 alone dropped, a3 keeping seq 3", got)
	}
	trs, _ := Reconstruct(got[0].Steps)
	if len(trs) != 1 || len(trs[0].Gaps) != 1 || trs[0].Gaps[0].First != 2 || trs[0].Gaps[0].Last != 2 {
		t.Errorf("the mutant's gaps %v, want recorder a's seq 2", trs[0].Gaps)
	}
	// only when named: by default its mutants (only ever undecided)
	// were noise in every report
	for _, m := range Mutations([]Step{a1, b1, a2, a3}) {
		if m.Op == "gap" {
			t.Errorf("a gap mutant by default: %v", m)
		}
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

// shapes are small logs of each shape a recorder's contract is about:
// the contract log; a restart onto a new recorder (two incarnations of
// one actor on two recorders); Lamport keys across actors on a thread
// that records two of them; watermarks and a settle; seqs absent.
func shapes() map[string][]Step {
	at := func(ms int) time.Time { return time.Unix(3000, 0).Add(time.Duration(ms) * time.Millisecond) }
	lamport := func(n int64) map[string]int64 { return map[string]int64{"lamport": n} }
	return map[string][]Step{
		"contract": contractLog(),
		"incarnations": {
			{Action: "a", Seq: 1, Recorder: "r1", Actor: "A", Process: "p1", Thread: "p1", Time: at(0)},
			{Action: "b", Seq: 2, Recorder: "r1", Actor: "A", Process: "p1", Thread: "p1", Time: at(2), Args: map[string]any{"n": int64(1)}},
			{Action: "c", Seq: 1, Recorder: "r2", Actor: "A", Process: "p2", Thread: "p2", Time: at(4)},
			{Action: "d", Seq: 2, Recorder: "r2", Actor: "A", Process: "p2", Thread: "p2", Time: at(6)},
			{Action: "e", Seq: 1, Recorder: "o", Actor: "B", Process: "o", Thread: "o", Time: at(3)},
		},
		"lamport": {
			{Action: "x", Seq: 1, Recorder: "r", Actor: "A", Process: "p", Thread: "t", Time: at(0), Order: lamport(5)},
			{Action: "z", Seq: 2, Recorder: "r", Actor: "B", Process: "p", Thread: "t", Time: at(1), Order: lamport(2)},
			{Action: "y", Seq: 3, Recorder: "r", Actor: "A", Process: "p", Thread: "t", Time: at(2), Order: lamport(6)},
			{Action: "w", Seq: 1, Recorder: "q", Actor: "B", Process: "q", Thread: "q", Time: at(3), Order: lamport(3), Args: map[string]any{"v": int64(7)}},
		},
		"watermarks": MarkComplete(MarkComplete([]Step{
			{Action: "a", Seq: 1, Recorder: "r", Actor: "A", Process: "r", Thread: "r", Time: at(0)},
			{Action: "b", Seq: 2, Recorder: "r", Actor: "A", Process: "r", Thread: "r", Time: at(2), Obs: map[string]any{"ok": true}},
			{Action: "c", Seq: 1, Recorder: "q", Actor: "B", Process: "q", Thread: "q", Time: at(3)},
			// at c's time, after it: c has no room to move
			{Action: "c2", Seq: 2, Recorder: "q", Actor: "B", Process: "q", Thread: "q", Time: at(3)},
			{Action: "settled", Seq: 3, Recorder: "r", Actor: "A", Process: "r", Thread: "r", Time: at(5), Settled: true},
		}, "r", at(5)), "q", at(5)),
		"no seqs": {
			{Action: "a", Recorder: "r", Actor: "A", Process: "r", Thread: "r", Time: at(0), Args: map[string]any{"s": ""}},
			// a watermark between them is in no sequence: a and b are adjacent
			{Action: "complete", Recorder: "r", Process: "r", Time: at(0), CompleteThrough: at(0)},
			{Action: "b", Recorder: "r", Actor: "A", Process: "r", Thread: "r", Time: at(1), Args: map[string]any{"f": 1.5}},
			{Action: "c", Recorder: "q", Actor: "B", Process: "q", Thread: "q", Time: at(2)},
		},
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

// naiveMutations enumerates every single mutation of steps the slow
// way, from the rules as the doc states them, sharing no code with
// Mutations: seqs renumbered by counting a recorder's records, not by
// shifting; swaps found by scanning every pair; a retime's window and a
// reactor's process found by scanning the whole log for each step; and
// every candidate whose log Reconstruct refuses with a new error left
// out.
func naiveMutations(steps []Step) []StepMutant {
	model := func(s Step) bool {
		return !s.Invocation && !s.Settled && s.Lost == "" && s.CompleteThrough.IsZero()
	}
	inSeq := func(s Step) bool { return s.Lost == "" && s.CompleteThrough.IsZero() }
	clone := func() []Step {
		c := make([]Step, len(steps))
		copy(c, steps)
		return c
	}
	// resequence renumbers recorder rec of log: its records with seqs
	// (in their order, which the caller arranged) numbered from the
	// first seq up, and calls and losses mapped to the new numbers
	resequence := func(log []Step, rec string, order []int, first uint64, oldOf map[int]uint64) {
		newOf := map[uint64]uint64{}
		for k, i := range order {
			if o, ok := oldOf[i]; ok {
				if _, seen := newOf[o]; !seen {
					newOf[o] = first + uint64(k)
				}
			}
			log[i].Seq = first + uint64(k)
		}
		for i := range log {
			s := &log[i]
			if s.Recorder != rec {
				continue
			}
			if s.Call != 0 {
				s.Call = newOf[s.Call]
			}
			if s.Lost != "" {
				// the number of records at or before the loss's seq
				n := uint64(0)
				for _, j := range order {
					if o, ok := oldOf[j]; ok && o <= s.LostAfter || !ok {
						n++
					}
				}
				s.LostAfter = first + n - 1
			}
		}
	}
	// seqOrder is rec's records with seqs, by seq, in log
	seqOrder := func(log []Step, rec string) []int {
		var idx []int
		for i := range log {
			if log[i].Recorder == rec && inSeq(log[i]) && log[i].Seq != 0 {
				idx = append(idx, i)
			}
		}
		slices.SortStableFunc(idx, func(a, b int) int { return int(log[a].Seq) - int(log[b].Seq) })
		return idx
	}
	var drops, dups, swaps, corrupts, retimes, reactors, gaps []StepMutant
	for i, s := range steps {
		if !model(s) {
			continue
		}
		// drop
		m := append(clone()[:i:i], steps[i+1:]...)
		if s.Seq != 0 {
			order := seqOrder(m, s.Recorder)
			old := map[int]uint64{}
			first := uint64(0)
			for _, j := range order {
				old[j] = m[j].Seq
				if first == 0 || m[j].Seq < first {
					first = m[j].Seq
				}
			}
			if len(order) > 0 && s.Seq < first {
				first = s.Seq
			}
			resequence(m, s.Recorder, order, first, old)
		}
		drops = append(drops, StepMutant{Op: "drop", Index: i, Action: s.Action, Steps: m})
		// dup
		m = append(append(clone()[:i+1:i+1], s), steps[i+1:]...)
		if s.Seq != 0 {
			order := seqOrder(m, s.Recorder)
			// the copy (at i+1) follows its original (at i) in the order
			order = slices.DeleteFunc(order, func(j int) bool { return j == i+1 })
			at := slices.Index(order, i)
			order = slices.Insert(order, at+1, i+1)
			old := map[int]uint64{}
			first := m[order[0]].Seq
			for _, j := range order {
				if j != i+1 {
					old[j] = m[j].Seq
				}
			}
			resequence(m, s.Recorder, order, first, old)
		}
		dups = append(dups, StepMutant{Op: "dup", Index: i, Action: s.Action, Steps: m})
	}
	// swap: every pair of model steps of one recorder and process with no
	// record of that recorder's sequence between them, and something to
	// tell them apart
	for i, a := range steps {
		for j, b := range steps {
			if j <= i || !model(a) || !model(b) || a.Recorder != b.Recorder || a.Process != b.Process {
				continue
			}
			lo, hi := min(a.Seq, b.Seq), max(a.Seq, b.Seq)
			between := false
			for k, o := range steps {
				if k != i && k != j && o.Recorder == a.Recorder && inSeq(o) && (a.Seq != 0 && o.Seq > lo && o.Seq < hi || a.Seq == 0 && k > i && k < j) {
					between = true
				}
			}
			if between || a.Seq != 0 && hi-lo != 1 {
				continue
			}
			x, y := a, b
			x.Seq, y.Seq, x.Time, y.Time, x.Invoked, y.Invoked = 0, 0, time.Time{}, time.Time{}, time.Time{}, time.Time{}
			if x.Call == 0 && y.Call == 0 && fmt.Sprint(x.Action, x.Actor, x.Thread, x.Outcome, x.Error, x.Ambiguous, x.Args, x.Obs, x.Order) == fmt.Sprint(y.Action, y.Actor, y.Thread, y.Outcome, y.Error, y.Ambiguous, y.Args, y.Obs, y.Order) {
				continue
			}
			m := clone()
			m[i], m[j] = b, a
			m[i].Seq, m[i].Time, m[i].Invoked = a.Seq, a.Time, a.Invoked
			m[j].Seq, m[j].Time, m[j].Invoked = b.Seq, b.Time, b.Invoked
			swaps = append(swaps, StepMutant{Op: "swap", Index: i, Action: a.Action, Steps: m})
		}
	}
	// corrupt
	for i, s := range steps {
		if !model(s) {
			continue
		}
		for _, obs := range []bool{false, true} {
			vals, prefix := s.Args, ""
			if obs {
				vals, prefix = s.Obs, "obs."
			}
			var keys []string
			for k := range vals {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				var changes []string
				var news []any
				switch v := vals[k].(type) {
				case int64:
					changes, news = []string{"+1", "-1"}, []any{v + 1, v - 1}
				case int:
					changes, news = []string{"+1", "-1"}, []any{v + 1, v - 1}
				case float64:
					changes, news = []string{"+1", "-1"}, []any{v + 1, v - 1}
				case bool:
					changes, news = []string{"flip"}, []any{!v}
				case string:
					if v == "" {
						changes, news = []string{"change"}, []any{"x"}
					} else {
						changes, news = []string{"empty", "change"}, []any{"", v + "~"}
					}
				}
				changes = append(changes, "remove")
				for c, ch := range changes {
					nv := map[string]any{}
					for kk, vv := range vals {
						if kk != k {
							nv[kk] = vv
						}
					}
					if ch != "remove" {
						nv[k] = news[c]
					}
					m := clone()
					if obs {
						m[i].Obs = nv
					} else {
						m[i].Args = nv
					}
					corrupts = append(corrupts, StepMutant{Op: "corrupt", Index: i, Action: s.Action, Field: prefix + k, Change: ch, Steps: m})
				}
			}
		}
	}
	// retime
	for i, s := range steps {
		if !model(s) {
			continue
		}
		// the earliest later step of its actor, settle or watermark
		end := -1
		for j, o := range steps {
			if (model(o) && o.Actor == s.Actor || o.Settled || !o.CompleteThrough.IsZero()) && o.Time.After(s.Time) && (end < 0 || o.Time.Before(steps[end].Time)) {
				end = j
			}
		}
		// a step of its actor at its time that follows it leaves no room
		tie := false
		for j, o := range steps {
			if j != i && model(o) && o.Actor == s.Actor && o.Time.Equal(s.Time) && (o.Recorder != s.Recorder || o.Seq > s.Seq) {
				tie = true
			}
		}
		if tie {
			continue
		}
		// other actors' steps after it and before that, by time then place
		var others []int
		for j, o := range steps {
			if model(o) && o.Actor != s.Actor && o.Time.After(s.Time) && (end < 0 || o.Time.Before(steps[end].Time)) {
				others = append(others, j)
			}
		}
		slices.SortStableFunc(others, func(a, b int) int { return steps[a].Time.Compare(steps[b].Time) })
		var at time.Time
		past := -1
		switch {
		case end >= 0 && len(others) > 0:
			past = others[len(others)-1]
			at = steps[past].Time.Add(steps[end].Time.Sub(steps[past].Time) / 2)
		case end >= 0:
			at = s.Time.Add(steps[end].Time.Sub(s.Time) / 2)
		case len(others) > 1:
			past = others[0]
			at = steps[past].Time.Add(steps[others[1]].Time.Sub(steps[past].Time) / 2)
		case len(others) == 1:
			past = others[0]
			at = steps[past].Time.Add(steps[past].Time.Sub(s.Time))
		default:
			continue
		}
		cp := s
		cp.Time = at
		if !s.Invoked.IsZero() {
			cp.Invoked = s.Invoked.Add(at.Sub(s.Time))
		}
		// the log with cp just after the step it passed (in place if none)
		var m []Step
		for j, o := range steps {
			switch {
			case j == i && past < 0:
				m = append(m, cp)
			case j == i:
			case j == past:
				m = append(m, o, cp)
			default:
				m = append(m, o)
			}
		}
		retimes = append(retimes, StepMutant{Op: "retime", Index: i, Action: s.Action, Steps: m})
	}
	// reactor
	var actors []string
	for _, s := range steps {
		if model(s) && !slices.Contains(actors, s.Actor) {
			actors = append(actors, s.Actor)
		}
	}
	slices.Sort(actors)
	if len(actors) > 1 {
		for i, s := range steps {
			if !model(s) {
				continue
			}
			to := actors[(slices.Index(actors, s.Actor)+1)%len(actors)]
			proc, best := "", -1
			for j, o := range steps {
				if model(o) && o.Actor == to && !o.Time.After(s.Time) && (best < 0 || !o.Time.Before(steps[best].Time)) {
					proc, best = o.Process, j
				}
			}
			if best < 0 {
				for _, o := range steps {
					if model(o) && o.Actor == to {
						proc = o.Process
						break
					}
				}
			}
			m := clone()
			m[i].Actor, m[i].Process = to, proc
			reactors = append(reactors, StepMutant{Op: "reactor", Index: i, Action: s.Action, Steps: m})
		}
	}
	// gap: a model step with a seq taken out, nothing renumbered, when
	// records of its recorder's sequence have a smaller and a larger seq
	for i, s := range steps {
		if !model(s) || s.Seq == 0 {
			continue
		}
		earlier, later := false, false
		for _, o := range steps {
			if o.Recorder == s.Recorder && inSeq(o) && o.Seq != 0 {
				earlier, later = earlier || o.Seq < s.Seq, later || o.Seq > s.Seq
			}
		}
		if earlier && later {
			gaps = append(gaps, StepMutant{Op: "gap", Index: i, Action: s.Action, Steps: append(clone()[:i:i], steps[i+1:]...)})
		}
	}
	kinds := func(log []Step) map[string]int {
		n := map[string]int{}
		trs, is := Reconstruct(log)
		for _, tr := range trs {
			is = append(is, tr.Issues...)
		}
		for _, x := range is {
			if x.Severity == "error" {
				n[x.Kind]++
			}
		}
		return n
	}
	base := kinds(steps)
	var out []StepMutant
	for _, ms := range [][]StepMutant{drops, dups, swaps, corrupts, retimes, reactors, gaps} {
		for _, m := range ms {
			ok := true
			for k, n := range kinds(m.Steps) {
				ok = ok && (n <= base[k] || m.Op == "gap" && k == "gap")
			}
			if ok {
				out = append(out, m)
			}
		}
	}
	return out
}

// Mutations is the naive enumeration of every single mutation, on each
// shape: the same mutants, in the same order, with the same logs.
func TestMutationsAgainstBruteForce(t *testing.T) {
	t.Parallel()
	small := []Step{
		{Action: "inc", Seq: 1, Recorder: "a", Actor: "A", Process: "a", Thread: "a", Time: time.Unix(1000, 0), Args: map[string]any{"n": int64(1)}},
		{Action: "put", Seq: 1, Recorder: "b", Actor: "B", Process: "b", Thread: "b", Time: time.Unix(1000, 5e6), Args: map[string]any{"k": "x"}},
		{Action: "get", Seq: 2, Recorder: "a", Actor: "A", Process: "a", Thread: "a", Time: time.Unix(1000, 10e6), Obs: map[string]any{"v": true}},
	}
	logs := shapes()
	logs["small"] = small
	for name, log := range logs {
		got, want := Mutations(log, MutateOps(MutationOps...)), naiveMutations(log)
		key := func(m StepMutant) string { return fmt.Sprintf("%s %d %s %s", m.Op, m.Index, m.Field, m.Change) }
		if len(got) != len(want) {
			var g, w []string
			for _, m := range got {
				g = append(g, key(m))
			}
			for _, m := range want {
				w = append(w, key(m))
			}
			t.Errorf("%s: %d mutants, the naive enumeration %d:\n got %v\nwant %v", name, len(got), len(want), g, w)
			continue
		}
		for i := range want {
			if key(got[i]) != key(want[i]) || got[i].Action != want[i].Action {
				t.Errorf("%s: mutant %d is %s, the naive enumeration's %s", name, i, key(got[i]), key(want[i]))
				continue
			}
			if !reflect.DeepEqual(got[i].Steps, want[i].Steps) {
				t.Errorf("%s: %s:\n got %v\nwant %v", name, got[i], got[i].Steps, want[i].Steps)
			}
		}
	}
}

// No mutant breaks the recorder's contract, on any shape: none raises an
// error issue the log itself does not (a gap, a duplicate, an overlap, a
// cycle, a completion before its invocation) and none unsettles a
// settled run, and each op changes what
// it says (a drop one model step fewer, a dup one more, a corruption one
// field, a retime nothing but its step's place and time, its actor's
// order kept). By construction: before the well-formedness filter, the
// ops make only the mutants a filter must leave out that they cannot
// see, a step moved into an actor whose Lamport keys contradict its
// thread's order (lamport's reactors of x and z).
func TestMutationsKeepTheRecorderContract(t *testing.T) {
	t.Parallel()
	models := func(steps []Step) int {
		n := 0
		for i := range steps {
			if steps[i].Model() {
				n++
			}
		}
		return n
	}
	raw := map[string]func([]Step) []StepMutant{"drop": drops, "dup": dups, "swap": swaps, "corrupt": corruptions, "retime": retimes, "reactor": reactors, "gap": holes}
	filtered := map[string]map[string]int{"lamport": {"reactor": 2}}
	for name, log := range shapes() {
		if base := reconstructErrors(log); len(base) > 0 {
			t.Fatalf("%s itself: %v", name, base)
		}
		for _, op := range MutationOps {
			ms := raw[op](log)
			exempt := ""
			if op == "gap" {
				exempt = "gap"
			}
			if left := len(ms) - len(wellFormed(log, ms, exempt)); left != filtered[name][op] {
				t.Errorf("%s: %d %s mutants Reconstruct refuses, want %d", name, left, op, filtered[name][op])
			}
		}
		settled := func(steps []Step) bool {
			trs, _ := Reconstruct(steps)
			for _, tr := range trs {
				if tr.Settled {
					return true
				}
			}
			return false
		}
		byOp := map[string]int{}
		for _, m := range Mutations(log, MutateOps(MutationOps...)) {
			byOp[m.Op]++
			// a settle still settles: no step moved past it
			if settled(log) && !settled(m.Steps) {
				t.Errorf("%s: %s unsettles the run", name, m)
			}
			errs := reconstructErrors(m.Steps)
			if m.Op == "gap" {
				// its one new issue is the hole it made, of the step's seq
				d := log[m.Index]
				want := fmt.Sprintf("missing seq [%d]", d.Seq)
				errs = slices.Compact(errs) // (one per actor of the recorder)
				if len(errs) != 1 || !strings.Contains(errs[0], want) || models(m.Steps) != models(log)-1 {
					t.Errorf("%s: %s: %v, want one gap issue (%s) and one model step fewer", name, m, errs, want)
				}
				errs = nil
			}
			if len(errs) > 0 {
				t.Errorf("%s: %s: %v", name, m, errs)
			}
			switch m.Op {
			case "drop":
				if models(m.Steps) != models(log)-1 {
					t.Errorf("%s: %s: %d model steps", name, m, models(m.Steps))
				}
			case "dup":
				if models(m.Steps) != models(log)+1 {
					t.Errorf("%s: %s: %d model steps", name, m, models(m.Steps))
				}
			case "corrupt":
				diff := 0
				for i := range log {
					if !reflect.DeepEqual(log[i], m.Steps[i]) {
						diff++
					}
				}
				if diff != 1 {
					t.Errorf("%s: %s: %d steps differ", name, m, diff)
				}
			case "retime":
				// its actor's steps keep their order in time
				s := log[m.Index]
				var moved Step
				for _, o := range m.Steps {
					if o.Model() && o.Recorder == s.Recorder && o.Seq == s.Seq && o.Action == s.Action && !o.Time.Equal(s.Time) {
						moved = o
					}
				}
				for _, o := range log {
					if o.Model() && o.Actor == s.Actor && o.Time.After(s.Time) && !moved.Time.Before(o.Time) {
						t.Errorf("%s: %s: moved to %s, past its actor's %s at %s", name, m, moved.Time, o.Action, o.Time)
					}
				}
			}
			// a recorder's loss and its completions name the seqs they did
			for i := range m.Steps {
				s := &m.Steps[i]
				if s.Lost != "" {
					last := uint64(0)
					for _, o := range m.Steps {
						if o.Recorder == s.Recorder && o.Lost == "" && o.CompleteThrough.IsZero() {
							last = max(last, o.Seq)
						}
					}
					if s.LostAfter != last {
						t.Errorf("%s: %s: %s's suffix lost after %d, its last seq %d", name, m, s.Recorder, s.LostAfter, last)
					}
				}
			}
		}
		// every op makes some mutant of each shape it applies to
		for _, op := range MutationOps {
			if op == "gap" && len(holes(log)) == 0 {
				continue // no recorder with a step between two of its records
			}
			if byOp[op] == 0 {
				t.Errorf("%s: no %s mutant", name, op)
			}
		}
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
