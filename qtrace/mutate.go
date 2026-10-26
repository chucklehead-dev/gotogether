package qtrace

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"
)

// A StepMutant is a recorded log with one mistake in it (Mutations): the
// log a recorder or a binding that got one field wrong would have
// produced. Judging it as the original was judged says whether the
// model and the binding constrain what it changed: a mutant whose verdict
// is the original's survived, and what it changed is something the check
// does not read.
type StepMutant struct {
	// Op is the mutation: "drop", "dup", "swap", "corrupt", "retime" or
	// "reactor" (MutationOps).
	Op string
	// Index is the step mutated, in the original steps (for a swap, the
	// earlier of the two).
	Index int
	// Action is that step's action.
	Action string
	// Field and Change are a corruption's: the field changed, as a binding
	// names it ("amount", "obs.balance"), and how ("+1", "-1", "flip",
	// "empty", "change", "remove"). Empty for the other ops.
	Field, Change string
	// Detail says what changed, for a report: the step by recorder and
	// seq, and the values.
	Detail string
	// Steps are the mutated log: the original's steps, one changed,
	// dropped, added or moved, and every recorder's sequence kept dense.
	Steps []Step
}

func (m StepMutant) String() string { return m.Op + " " + m.Detail }

// The mutations, in the order Mutations makes them.
// Read-only.
var MutationOps = []string{"drop", "dup", "swap", "corrupt", "retime", "reactor"}

// A MutationOption narrows Mutations.
type MutationOption func(*mutationConfig)

type mutationConfig struct {
	ops   map[string]bool // nil: every op
	limit int             // per op; 0: none
}

// MutateOps makes only these ops' mutants (MutationOps names them). An
// unknown op panics, naming the ops: a misspelled one would make none.
func MutateOps(ops ...string) MutationOption {
	for _, op := range ops {
		if !slices.Contains(MutationOps, op) {
			panic(fmt.Sprintf("qtrace: MutateOps(%q): no such mutation; the ops are %s", op, strings.Join(MutationOps, ", ")))
		}
	}
	return func(c *mutationConfig) {
		if c.ops == nil {
			c.ops = map[string]bool{}
		}
		for _, op := range ops {
			c.ops[op] = true
		}
	}
}

// MutateLimit keeps at most n mutants of each op, spread evenly over the
// log (every k-th of the op's, from the first), not its first n: a long
// log's mutants of its head alone would say nothing of its tail. n <= 0
// keeps every one.
func MutateLimit(n int) MutationOption { return func(c *mutationConfig) { c.limit = n } }

// Mutations are steps' single mutations, each a whole log (StepMutant),
// in a fixed order: by op (drop, dup, swap, corrupt, retime, reactor),
// then by the step's index, then by field and change. Only model steps
// (Step.Model) are mutated; invocation, settle and loss records stay, and
// are renumbered with their recorder's steps. Each op keeps the
// recorder's own contract, so that the check judging a mutant is the
// model's and the binding's, not Reconstruct's gap or duplicate check:
//
//   - drop: one step taken out; its recorder's later seqs (and the calls
//     and losses naming them) move down one, so no gap shows. A gap is a
//     lost step, which every check already reports.
//   - dup: one step recorded twice; the copy follows it with the next seq
//     of its recorder, the later ones moving up one. A copy with the same
//     seq is what an exporter's retry makes, and Reconstruct drops it
//     ("seen twice; kept the first"): it would test the dedupe, not the
//     model. The copy of a completion completes the same call again.
//   - swap: two steps of one recorder, adjacent in its sequence (no
//     record between them), exchange their seqs, times and invocation
//     times, so their order changes and each keeps its content. Steps
//     alike in everything but their place are not swapped, nor steps of
//     two incarnations: the old one's step after the new one's is an
//     overlap, which Reconstruct reports.
//   - corrupt: one argument or observation changed: an integer or float
//     +1 and -1, a bool flipped, a string emptied (if not empty) and
//     changed, and every field removed (absent), each its own mutant.
//   - retime: one step's time (and its invocation time, kept as long
//     before it) moved later, never past its actor's next step (a
//     within's clock would go back), nor past a settle or a watermark
//     record: past every other actor's step before its actor's next, to
//     the middle of the last of them and that step; with none between,
//     to the middle of its gap, for a within; its actor's last, past
//     the next step of another actor (to the middle of it and the one
//     after, or as far after it as it was before). It moves in the slice
//     to just after the step it moved past. For bindings with within and
//     for real-time order (Linearize); a check that reads no time is
//     blind to it.
//   - reactor: one step moved to the next actor (in sorted order), when
//     the log has several, in that actor's process at the time (its
//     latest step's at or before, else its first), so no actor shows two
//     incarnations at once.
//
// A mutant whose log Reconstruct would refuse with an error issue the
// original's lacks is left out (wellFormed): a step moved into an actor
// whose order keys contradict its thread's program order (a cycle), or
// any other the ops cannot see. A mutant is a question for the model and
// the binding, not for Reconstruct.
func Mutations(steps []Step, opts ...MutationOption) []StepMutant {
	var c mutationConfig
	for _, o := range opts {
		o(&c)
	}
	var out []StepMutant
	for _, op := range MutationOps {
		if c.ops != nil && !c.ops[op] {
			continue
		}
		var ms []StepMutant
		switch op {
		case "drop":
			ms = drops(steps)
		case "dup":
			ms = dups(steps)
		case "swap":
			ms = swaps(steps)
		case "corrupt":
			ms = corruptions(steps)
		case "retime":
			ms = retimes(steps)
		case "reactor":
			ms = reactors(steps)
		}
		out = append(out, spread(wellFormed(steps, ms), c.limit)...)
	}
	return out
}

// wellFormed are the mutants of ms whose logs Reconstruct raises no
// error issue of a kind (or more of one) the original's lacks: the ops
// keep a recorder's contract by construction, and what they cannot see
// (a step moved into an actor whose order keys contradict its thread's
// program order: a cycle) is left out here, so every mutant is judged by
// the model and the binding, never stopped at Reconstruct.
func wellFormed(steps []Step, ms []StepMutant) []StepMutant {
	base := errorKinds(steps)
	out := ms[:0:0]
	for _, m := range ms {
		ok := true
		for k, n := range errorKinds(m.Steps) {
			if n > base[k] {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, m)
		}
	}
	return out
}

// errorKinds counts the error issues Reconstruct raises of steps, by
// kind.
func errorKinds(steps []Step) map[string]int {
	traces, issues := Reconstruct(steps)
	for _, t := range traces {
		issues = append(issues, t.Issues...)
	}
	n := map[string]int{}
	for _, is := range issues {
		if is.Severity == "error" {
			n[is.Kind]++
		}
	}
	return n
}

// spread is at most n of ms, evenly spaced from the first.
func spread(ms []StepMutant, n int) []StepMutant {
	if n <= 0 || len(ms) <= n {
		return ms
	}
	out := make([]StepMutant, 0, n)
	for k := range n {
		out = append(out, ms[k*len(ms)/n])
	}
	return out
}

// where names a step for a mutant's detail: its action, recorder and seq.
func (s *Step) where() string {
	if s.Seq == 0 {
		return fmt.Sprintf("%s (recorder %s)", s.Action, s.Recorder)
	}
	return fmt.Sprintf("%s (recorder %s seq %d)", s.Action, s.Recorder, s.Seq)
}

// renumber moves every seq of recorder rec's records above from by
// delta, and the calls and losses that name them: what keeps a sequence
// dense when a step is taken out (delta -1) or put in (+1).
func renumber(steps []Step, rec string, from uint64, delta int) {
	move := func(q *uint64) {
		if *q > from {
			*q = uint64(int64(*q) + int64(delta))
		}
	}
	for i := range steps {
		s := &steps[i]
		if s.Recorder != rec {
			continue
		}
		if s.Lost != "" {
			// a suffix lost after the step itself is lost after its
			// copy, or after the step before it once it is dropped; a
			// prefix is lost up to before the first step, which no op
			// moves
			if s.LostAfter >= from {
				s.LostAfter = uint64(int64(s.LostAfter) + int64(delta))
			}
			continue
		}
		move(&s.Seq)
		if s.Call != 0 {
			move(&s.Call)
		}
	}
}

func drops(steps []Step) []StepMutant {
	var out []StepMutant
	for i := range steps {
		s := &steps[i]
		if !s.Model() {
			continue
		}
		m := slices.Delete(slices.Clone(steps), i, i+1)
		detail := "of " + s.where()
		if s.Seq != 0 {
			renumber(m, s.Recorder, s.Seq, -1)
			detail += ", the recorder's later seqs moved down one"
		}
		out = append(out, StepMutant{Op: "drop", Index: i, Action: s.Action, Detail: detail, Steps: m})
	}
	return out
}

func dups(steps []Step) []StepMutant {
	var out []StepMutant
	for i := range steps {
		s := &steps[i]
		if !s.Model() {
			continue
		}
		m := slices.Clone(steps)
		cp := *s
		detail := "of " + s.where()
		if s.Seq != 0 {
			renumber(m, s.Recorder, s.Seq, +1)
			cp.Seq = s.Seq + 1
			detail += fmt.Sprintf(", the copy seq %d", cp.Seq)
		}
		m = slices.Insert(m, i+1, cp)
		out = append(out, StepMutant{Op: "dup", Index: i, Action: s.Action, Detail: detail, Steps: m})
	}
	return out
}

// alike reports whether two steps differ only in their place (seq,
// time): swapping them changes nothing a check reads.
func alike(a, b *Step) bool {
	return a.Action == b.Action && a.Actor == b.Actor && a.Process == b.Process && a.Thread == b.Thread &&
		a.Outcome == b.Outcome && a.Error == b.Error && a.Call == 0 && b.Call == 0 && a.Ambiguous == b.Ambiguous &&
		maps.Equal(a.Args, b.Args) && maps.Equal(a.Obs, b.Obs) && maps.Equal(a.Order, b.Order)
}

func swaps(steps []Step) []StepMutant {
	// each recorder's records in its sequence: seq, then place in the log
	byRec := map[string][]int{}
	for i := range steps {
		if steps[i].Lost == "" && steps[i].CompleteThrough.IsZero() { // a loss or a watermark is in no sequence
			byRec[steps[i].Recorder] = append(byRec[steps[i].Recorder], i)
		}
	}
	var pairs [][2]int
	for _, idx := range byRec {
		sort.SliceStable(idx, func(a, b int) bool { return steps[idx[a]].Seq < steps[idx[b]].Seq })
		for k := 0; k+1 < len(idx); k++ {
			a, b := &steps[idx[k]], &steps[idx[k+1]]
			if a.Model() && b.Model() && a.Process == b.Process && !alike(a, b) {
				pairs = append(pairs, [2]int{min(idx[k], idx[k+1]), max(idx[k], idx[k+1])})
			}
		}
	}
	sort.Slice(pairs, func(a, b int) bool {
		return pairs[a][0] < pairs[b][0] || pairs[a][0] == pairs[b][0] && pairs[a][1] < pairs[b][1]
	})
	var out []StepMutant
	for _, p := range pairs {
		m := slices.Clone(steps)
		a, b := &m[p[0]], &m[p[1]]
		detail := fmt.Sprintf("of %s and %s", steps[p[0]].where(), steps[p[1]].where())
		a.Seq, b.Seq = b.Seq, a.Seq
		a.Time, b.Time = b.Time, a.Time
		a.Invoked, b.Invoked = b.Invoked, a.Invoked
		m[p[0]], m[p[1]] = m[p[1]], m[p[0]] // and their places, for a log with no seqs
		out = append(out, StepMutant{Op: "swap", Index: p[0], Action: steps[p[0]].Action, Detail: detail, Steps: m})
	}
	return out
}

// corrupted are the changes of one value: what each change makes it, by
// change; removal is every field's, added by the caller.
func corrupted(v any) [][2]any {
	switch x := v.(type) {
	case int64:
		return [][2]any{{"+1", x + 1}, {"-1", x - 1}}
	case int:
		return [][2]any{{"+1", x + 1}, {"-1", x - 1}}
	case float64:
		return [][2]any{{"+1", x + 1}, {"-1", x - 1}}
	case bool:
		return [][2]any{{"flip", !x}}
	case string:
		if x == "" {
			return [][2]any{{"change", "x"}}
		}
		return [][2]any{{"empty", ""}, {"change", x + "~"}}
	}
	return nil
}

func corruptions(steps []Step) []StepMutant {
	var out []StepMutant
	for i := range steps {
		s := &steps[i]
		if !s.Model() {
			continue
		}
		for _, obs := range []bool{false, true} {
			vals, prefix := s.Args, ""
			if obs {
				vals, prefix = s.Obs, "obs."
			}
			for _, k := range slices.Sorted(maps.Keys(vals)) {
				v := vals[k]
				field := prefix + k
				changes := append(corrupted(v), [2]any{"remove", nil})
				for _, ch := range changes {
					m := slices.Clone(steps)
					t := &m[i]
					nv := maps.Clone(vals)
					detail := fmt.Sprintf("%s of %s: %v -> %v", field, s.where(), fmtValue(v), fmtValue(ch[1]))
					if ch[0] == "remove" {
						delete(nv, k)
						detail = fmt.Sprintf("%s of %s: %v removed", field, s.where(), fmtValue(v))
					} else {
						nv[k] = ch[1]
					}
					if obs {
						t.Obs = nv
					} else {
						t.Args = nv
					}
					out = append(out, StepMutant{Op: "corrupt", Index: i, Action: s.Action, Field: field, Change: ch[0].(string), Detail: detail, Steps: m})
				}
			}
		}
	}
	return out
}

func fmtValue(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprint(v)
}

func retimes(steps []Step) []StepMutant {
	var out []StepMutant
	for i := range steps {
		s := &steps[i]
		if !s.Model() {
			continue
		}
		// the window it may move in: up to its actor's next step, and to
		// the next settle or watermark record after it (a step past a
		// settle unsettles the run, which is no question of its time)
		var end *Step
		earlier := func(o *Step) bool { return o.Time.After(s.Time) && (end == nil || o.Time.Before(end.Time)) }
		tied := false // a step of its actor at its very time, after it: no room to move
		for j := range steps {
			o := &steps[j]
			if (o.Model() && o.Actor == s.Actor || o.Settled || !o.CompleteThrough.IsZero()) && earlier(o) {
				end = o
			}
			tied = tied || j != i && o.Model() && o.Actor == s.Actor && o.Time.Equal(s.Time) && (o.Recorder != s.Recorder || o.Seq > s.Seq)
		}
		if tied {
			continue
		}
		// the other actors' steps in it, in time
		var others []int
		for j := range steps {
			o := &steps[j]
			if o.Model() && o.Actor != s.Actor && o.Time.After(s.Time) && (end == nil || o.Time.Before(end.Time)) {
				others = append(others, j)
			}
		}
		sort.SliceStable(others, func(a, b int) bool { return steps[others[a]].Time.Before(steps[others[b]].Time) })
		var at time.Time
		past := -1 // the step it moves past, in the slice
		var detail string
		switch {
		case end != nil && len(others) > 0:
			// past every other actor's step before its actor's next
			past = others[len(others)-1]
			at = steps[past].Time.Add(end.Time.Sub(steps[past].Time) / 2)
			detail = fmt.Sprintf("past %s, before %s", steps[past].where(), end.where())
		case end != nil:
			// later within its gap, for a within
			at = s.Time.Add(end.Time.Sub(s.Time) / 2)
			detail = "before " + end.where()
		case len(others) > 0:
			// its actor's last: past the next step of another
			past = others[0]
			n := &steps[past]
			at = n.Time.Add(n.Time.Sub(s.Time))
			if len(others) > 1 {
				at = n.Time.Add(steps[others[1]].Time.Sub(n.Time) / 2)
			}
			detail = "past " + n.where()
		default:
			continue // the last in time: nothing to move past
		}
		delta := at.Sub(s.Time)
		cp := *s
		cp.Time = at
		if !cp.Invoked.IsZero() {
			cp.Invoked = cp.Invoked.Add(delta)
		}
		m := slices.Clone(steps)
		m[i] = cp
		// placed just after the step it moved past, so the log's order
		// says what its times say
		if past > i {
			m = slices.Insert(slices.Delete(m, i, i+1), past, cp)
		} else if past >= 0 {
			m = slices.Insert(slices.Delete(m, i, i+1), past+1, cp)
		}
		out = append(out, StepMutant{Op: "retime", Index: i, Action: s.Action,
			Detail: fmt.Sprintf("of %s: %s later, %s", s.where(), delta, detail), Steps: m})
	}
	return out
}

func reactors(steps []Step) []StepMutant {
	actors := map[string]bool{}
	for i := range steps {
		if steps[i].Model() {
			actors[steps[i].Actor] = true
		}
	}
	if len(actors) < 2 {
		return nil
	}
	names := slices.Sorted(maps.Keys(actors))
	// processOf is actor a's process at time t: its latest model step's
	// at or before t, else its first's
	processOf := func(a string, t time.Time) string {
		var proc string
		var at time.Time
		first := ""
		for i := range steps {
			o := &steps[i]
			if !o.Model() || o.Actor != a {
				continue
			}
			if first == "" {
				first = o.Process
			}
			if !o.Time.After(t) && (proc == "" || !o.Time.Before(at)) {
				proc, at = o.Process, o.Time
			}
		}
		if proc == "" {
			return first
		}
		return proc
	}
	var out []StepMutant
	for i := range steps {
		s := &steps[i]
		if !s.Model() {
			continue
		}
		to := names[(slices.Index(names, s.Actor)+1)%len(names)]
		m := slices.Clone(steps)
		m[i].Actor, m[i].Process = to, processOf(to, s.Time)
		out = append(out, StepMutant{Op: "reactor", Index: i, Action: s.Action,
			Detail: fmt.Sprintf("of %s: actor %s -> %s (process %s)", s.where(), s.Actor, to, m[i].Process), Steps: m})
	}
	return out
}
