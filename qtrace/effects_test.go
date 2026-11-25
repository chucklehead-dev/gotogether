package qtrace

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
)

// effectsLog is one recorder's run with effects: a pay call whose two
// tries are made under its scope (rule 1) and one more after it
// completed (late), a step whose task made two effects before it (rule
// 2, its Made), an effect no step claims, and a pending call with its
// send.
func effectsLog() []Step {
	t0 := time.Unix(100, 0)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Millisecond) }
	eff := func(seq, call uint64, kind string, args map[string]any) Step {
		return Step{Action: "effect", Seq: seq, Recorder: "r", Actor: "A", Process: "r", Time: at(int(seq)), Call: call, Record: Record{Effect: kind}, Args: args}
	}
	return []Step{
		{Action: "pay", Seq: 1, Recorder: "r", Actor: "A", Process: "r", Time: at(1), Record: Record{Invocation: true}, Args: map[string]any{"order": "o1"}},
		eff(2, 1, "rpc.p.request", map[string]any{"Amount": int64(10)}),
		eff(3, 1, "rpc.p.request", map[string]any{"Amount": int64(10)}),
		{Action: "pay", Seq: 4, Recorder: "r", Actor: "A", Process: "r", Time: at(4), Call: 1, Outcome: "ok", EffectsObserved: true, Args: map[string]any{"order": "o1"}},
		eff(5, 1, "queue.q.publish", map[string]any{"value": "late"}),
		eff(6, 0, "rpc.p.request", map[string]any{"Amount": int64(1)}),
		eff(7, 0, "rpc.p.request", map[string]any{"Amount": int64(2)}),
		{Action: "mark", Seq: 8, Recorder: "r", Actor: "A", Process: "r", Time: at(8), Outcome: "ok", EffectsObserved: true, Made: []uint64{6, 7}},
		eff(9, 0, "rpc.p.request", map[string]any{"Amount": int64(3)}),
		{Action: "pay", Seq: 10, Recorder: "r", Actor: "A", Process: "r", Time: at(10), Record: Record{Invocation: true}, Args: map[string]any{"order": "o2"}},
		eff(11, 10, "rpc.p.request", map[string]any{"Amount": int64(20)}),
		{Action: "mark", Seq: 12, Recorder: "r", Actor: "A", Process: "r", Time: at(12), Outcome: "ok", EffectsObserved: true},
	}
}

// Reconstruct takes effect records out of the steps and attaches each to
// the step it is attributed to: rule 1, the completion of the call it was
// made under (late after it), or the pending call; rule 2, the step
// whose Made names it; any other is made by no step. An effect naming an
// invocation does not complete it, and the steps' seqs stay dense.
func TestReconstructAttachesEffects(t *testing.T) {
	t.Parallel()
	trs, issues := Reconstruct(effectsLog())
	for _, is := range issues {
		if is.Severity == "error" {
			t.Errorf("issue %s", is)
		}
	}
	if len(trs) != 1 {
		t.Fatalf("%d traces", len(trs))
	}
	tr := trs[0]
	for _, is := range tr.Issues {
		t.Errorf("trace issue %s", is)
	}
	type got struct {
		seq  uint64
		by   string
		late bool
	}
	seqs := func(es []Effect) (out []got) {
		for _, e := range es {
			out = append(out, got{e.Seq, e.By, e.Late})
		}
		return out
	}
	if len(tr.Steps) != 3 || tr.Steps[0].Action != "pay" || tr.Steps[1].Action != "mark" || tr.Steps[2].Action != "mark" {
		t.Fatalf("steps %v", tr.Steps)
	}
	if g, want := seqs(tr.Steps[0].Effects), []got{{2, "call", false}, {3, "call", false}, {5, "call", true}}; !reflect.DeepEqual(g, want) {
		t.Errorf("pay's effects %v, want %v", g, want)
	}
	if g, want := seqs(tr.Steps[1].Effects), []got{{6, "task", false}, {7, "task", false}}; !reflect.DeepEqual(g, want) {
		t.Errorf("mark's effects %v, want %v", g, want)
	}
	if len(tr.Steps[2].Effects) != 0 || !tr.Steps[2].EffectsObserved {
		t.Errorf("the last mark made none, observed: %v", tr.Steps[2])
	}
	if len(tr.Pending) != 1 || !reflect.DeepEqual(seqs(tr.Pending[0].Effects), []got{{11, "call", false}}) {
		t.Errorf("the pending call and its send: %v", tr.Pending)
	}
	if g := seqs(tr.Unattributed); !reflect.DeepEqual(g, []got{{9, "", false}}) {
		t.Errorf("unattributed %v, want seq 9", g)
	}
	if f := tr.Steps[0].Effects[0].Fields["Amount"]; f != int64(10) {
		t.Errorf("an effect's fields are its record's: %v", f)
	}
}

// A step log keeps effect records and a step's Made through its encoding,
// byte for byte as encoding/json writes them, and reads them back.
func TestEffectsRoundTrip(t *testing.T) {
	t.Parallel()
	log := effectsLog()
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, log); err != nil {
		t.Fatal(err)
	}
	back, err := ReadJSONL(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(log) {
		t.Fatalf("%d records back, %d written", len(back), len(log))
	}
	for i := range log {
		b := back[i]
		b.Source = ""
		if !SameRecord(&log[i], &b) {
			t.Errorf("record %d: wrote %+v, read %+v", i, log[i], b)
		}
	}
	if !back[7].EffectsObserved || !reflect.DeepEqual(back[7].Made, []uint64{6, 7}) || back[1].Effect != "rpc.p.request" || back[1].Call != 1 {
		t.Errorf("read back: %+v %+v", back[7], back[1])
	}
}

// Two runs whose effects differ have two verdict keys: a cached verdict
// does not answer for another run's effects.
func TestEffectsInTheKey(t *testing.T) {
	t.Parallel()
	a, b := effectsLog(), effectsLog()
	b[1].Args = map[string]any{"Amount": int64(1)}
	if Key(a) == Key(b) {
		t.Error("an effect's field changed, the key the same")
	}
	c := effectsLog()
	c[7].Made = []uint64{6}
	if Key(a) == Key(c) {
		t.Error("a step's Made changed, the key the same")
	}
}

// The effect mutants keep the recorder's contract (Reconstruct raises no
// new error) and change what they say: a drop takes the record and its
// seq out of the step that named it, a dup's copy is the same step's, a
// corruption changes one field.
func TestEffectMutations(t *testing.T) {
	t.Parallel()
	log := effectsLog()
	byOp := map[string]int{}
	for _, m := range Mutations(log, MutateOps("effect-drop", "effect-dup", "effect-swap", "effect-corrupt")) {
		byOp[m.Op]++
		if errs := reconstructErrors(m.Steps); len(errs) > 0 {
			t.Errorf("%s: %v", m, errs)
		}
		trs, _ := Reconstruct(m.Steps)
		n := 0
		for _, tr := range trs {
			for _, s := range tr.Steps {
				n += len(s.Effects)
			}
			for _, s := range tr.Pending {
				n += len(s.Effects)
			}
			n += len(tr.Unattributed)
		}
		want := 7 // the log's
		switch m.Op {
		case "effect-drop":
			want--
		case "effect-dup":
			want++
		}
		if n != want {
			t.Errorf("%s: %d effects attached or listed, want %d", m, n, want)
		}
	}
	for _, op := range []string{"effect-drop", "effect-dup", "effect-swap", "effect-corrupt"} {
		if byOp[op] == 0 {
			t.Errorf("no %s mutant", op)
		}
	}
	// the original is untouched: renumbering copies the Made it changes
	if !reflect.DeepEqual(log[7].Made, []uint64{6, 7}) {
		t.Errorf("a mutant changed the original's Made: %v", log[7].Made)
	}
}

// An effect is named in a report as its kind, its fields in sorted order
// and its seq: what an I/O mismatch prints, so its exact form is the
// contract.
func TestEffectString(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		e    Effect
		want string
	}{
		{Effect{Kind: "queue.q.publish", Seq: 3}, "queue.q.publish() at seq 3"},
		{Effect{Kind: "rpc.p.request", Fields: map[string]any{"Amount": int64(10)}, Seq: 2}, "rpc.p.request(Amount=10) at seq 2"},
		{Effect{Kind: "rpc.p.request", Fields: map[string]any{"z": "last", "a": 1, "m": true}, Seq: 17}, "rpc.p.request(a=1, m=true, z=last) at seq 17"},
	} {
		if g := c.e.String(); g != c.want {
			t.Errorf("%#v: %q, want %q", c.e, g, c.want)
		}
	}
}

// A step's effects are in the order they were made (seq), whichever rule
// attributed them and in whatever order the log lists them: a completion
// whose task made one before the call and whose call made two, the log's
// records out of order, and a pending call's sends listed backwards.
func TestReconstructOrdersEffectsBySeq(t *testing.T) {
	t.Parallel()
	t0 := time.Unix(100, 0)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Millisecond) }
	eff := func(seq, call uint64) Step {
		return Step{Action: "effect", Seq: seq, Recorder: "r", Actor: "A", Process: "r", Time: at(int(seq)), Call: call, Record: Record{Effect: "rpc.p.request"}, Args: map[string]any{"n": int64(seq)}}
	}
	log := []Step{
		eff(4, 2),
		{Action: "pay", Seq: 2, Recorder: "r", Actor: "A", Process: "r", Time: at(2), Record: Record{Invocation: true}},
		eff(1, 0),
		eff(3, 2),
		{Action: "pay", Seq: 5, Recorder: "r", Actor: "A", Process: "r", Time: at(5), Call: 2, Outcome: "ok", EffectsObserved: true, Made: []uint64{1}},
		eff(8, 6),
		eff(7, 6),
		{Action: "pay", Seq: 6, Recorder: "r", Actor: "A", Process: "r", Time: at(6), Record: Record{Invocation: true}},
	}
	trs, issues := Reconstruct(log)
	for _, is := range issues {
		if is.Severity == "error" {
			t.Errorf("issue %s", is)
		}
	}
	if len(trs) != 1 || len(trs[0].Steps) != 1 || len(trs[0].Pending) != 1 {
		t.Fatalf("traces %+v", trs)
	}
	seqs := func(es []Effect) (out []uint64) {
		for _, e := range es {
			out = append(out, e.Seq)
		}
		return out
	}
	if g := seqs(trs[0].Steps[0].Effects); !reflect.DeepEqual(g, []uint64{1, 3, 4}) {
		t.Errorf("the completion's effects at seqs %v, want 1 3 4", g)
	}
	if g := seqs(trs[0].Pending[0].Effects); !reflect.DeepEqual(g, []uint64{7, 8}) {
		t.Errorf("the pending call's effects at seqs %v, want 7 8", g)
	}
}

// madeOf is the Made of the first step of action a in steps.
func madeOf(steps []Step, a string) []uint64 {
	for _, s := range steps {
		if s.Action == a {
			return s.Made
		}
	}
	return nil
}

// An effect-drop names the step the effect is attributed to (its call's
// completion, the step whose Made names it, else its kind) and keeps
// Made naming the same records: the dropped seq out of it, the later
// ones down one. An effect-dup's copy joins the Made of its original.
func TestEffectDropsAndDupsRenumberMade(t *testing.T) {
	t.Parallel()
	log := effectsLog() // the mark at index 7 made seqs 6 and 7
	type want struct {
		index  int
		action string
		made   []uint64
	}
	var got []want
	for _, m := range Mutations(log, MutateOps("effect-drop")) {
		got = append(got, want{m.Index, m.Action, madeOf(m.Steps, "mark")})
	}
	if w := []want{
		{1, "pay", []uint64{5, 6}},
		{2, "pay", []uint64{5, 6}},
		{4, "pay", []uint64{5, 6}},
		{5, "mark", []uint64{6}},
		{6, "mark", []uint64{6}},
		{8, "rpc.p.request", []uint64{6, 7}},
		{10, "rpc.p.request", []uint64{6, 7}},
	}; !reflect.DeepEqual(got, w) {
		t.Errorf("effect-drops %v, want %v", got, w)
	}
	got = nil
	for _, m := range Mutations(log, MutateOps("effect-dup")) {
		got = append(got, want{m.Index, m.Action, madeOf(m.Steps, "mark")})
	}
	if w := []want{
		{1, "pay", []uint64{7, 8}},
		{2, "pay", []uint64{7, 8}},
		{4, "pay", []uint64{7, 8}},
		{5, "mark", []uint64{6, 7, 8}},
		{6, "mark", []uint64{6, 7, 8}},
		{8, "rpc.p.request", []uint64{6, 7}},
		{10, "rpc.p.request", []uint64{6, 7}},
	}; !reflect.DeepEqual(got, w) {
		t.Errorf("effect-dups %v, want %v", got, w)
	}
	// a step's drop renumbers the Made of a later step too
	for _, m := range Mutations(log, MutateOps("drop")) {
		if m.Index == 3 { // pay's completion, seq 4
			if g := madeOf(m.Steps, "mark"); !reflect.DeepEqual(g, []uint64{5, 6}) {
				t.Errorf("pay's completion dropped: the mark's Made %v, want 5 6", g)
			}
			return
		}
	}
	t.Error("no drop of pay's completion")
}

// An effect-corrupt's removal takes the field out of the record, and
// its detail says so; its other changes set the field to the new value.
func TestEffectCorruptions(t *testing.T) {
	t.Parallel()
	log := effectsLog()
	n := 0
	for _, m := range Mutations(log, MutateOps("effect-corrupt")) {
		args := m.Steps[m.Index].Args
		v, ok := args[m.Field]
		switch m.Change {
		case "remove":
			if ok {
				t.Errorf("%s: %s still %v", m, m.Field, v)
			}
			if !strings.HasSuffix(m.Detail, " removed") {
				t.Errorf("%s: a removal's detail", m)
			}
		default:
			if !ok || reflect.DeepEqual(v, log[m.Index].Args[m.Field]) {
				t.Errorf("%s: %s is %v", m, m.Field, v)
			}
		}
		if m.Index == 1 && m.Change == "remove" {
			n++
			if want := "Amount of effect rpc.p.request (recorder r seq 2): 10 removed"; m.Detail != want || m.Action != "pay" {
				t.Errorf("%s by %s, want %q by pay", m.Detail, m.Action, want)
			}
		}
	}
	if n != 1 {
		t.Errorf("%d removals of seq 2's Amount", n)
	}
}

// swapLog is recorder rec's run, its effects in three pairs: two made
// under one call, two named by one step's Made (both swapped), and two
// made by no step (not swapped: nothing says they are one cause's).
func swapLog(rec string) []Step {
	t0 := time.Unix(100, 0)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Millisecond) }
	eff := func(seq, call uint64) Step {
		return Step{Action: "effect", Seq: seq, Recorder: rec, Actor: rec, Process: rec, Time: at(int(seq)), Call: call, Record: Record{Effect: "rpc.p.request"}, Args: map[string]any{"n": int64(seq)}}
	}
	return []Step{
		{Action: "pay", Seq: 1, Recorder: rec, Actor: rec, Process: rec, Time: at(1), Record: Record{Invocation: true}},
		eff(2, 1),
		eff(3, 1),
		{Action: "pay", Seq: 4, Recorder: rec, Actor: rec, Process: rec, Time: at(4), Call: 1, Outcome: "ok"},
		eff(5, 0),
		eff(6, 0),
		{Action: "mark", Seq: 7, Recorder: rec, Actor: rec, Process: rec, Time: at(7), Outcome: "ok"},
		eff(8, 0),
		eff(9, 0),
		{Action: "mark", Seq: 10, Recorder: rec, Actor: rec, Process: rec, Time: at(10), Outcome: "ok", Made: []uint64{8, 9}},
	}
}

// effect-swap exchanges the contents of two effects adjacent in their
// recorder's seqs and of one cause, whatever order the log lists them
// in, and its mutants are in the log's order: five recorders' (whose
// pairs are found per recorder) interleaved, every recorder's first half
// before any's second, so that one recorder's mutants after another's
// are out of order whichever recorder comes first; one recorder listed
// out of order.
func TestEffectSwaps(t *testing.T) {
	t.Parallel()
	recs := []string{"r1", "r2", "r3", "r4", "r5"}
	half := len(swapLog("")) / 2 // 5: the call's pair in the first, the Made's in the second
	var log []Step
	for _, rec := range recs {
		log = append(log, swapLog(rec)[:half]...)
	}
	for _, rec := range recs {
		log = append(log, swapLog(rec)[half:]...)
	}
	// r3's listed with its seqs 3 and 5 exchanged in place, so its pair
	// under the call is not adjacent in the log
	r3 := 2 * half
	log[r3+2], log[r3+4] = log[r3+4], log[r3+2]
	type swap struct {
		index  int
		action string
		seqs   [2]uint64
	}
	var got []swap
	for _, m := range Mutations(log, MutateOps("effect-swap")) {
		var ch []uint64
		for i := range log {
			if !reflect.DeepEqual(log[i].Args, m.Steps[i].Args) {
				ch = append(ch, log[i].Seq)
			}
		}
		if len(ch) != 2 {
			t.Fatalf("%s changed seqs %v", m, ch)
		}
		got = append(got, swap{m.Index, m.Action, [2]uint64{ch[0], ch[1]}})
	}
	var want []swap
	for k := range recs { // the calls' pairs: seq 2 at k*half+1 (r3's seq 3 at +4)
		want = append(want, swap{k*half + 1, "pay", [2]uint64{2, 3}})
	}
	for k := range recs { // the Made's pairs: seq 8 at the second half's +2
		want = append(want, swap{len(recs)*half + k*half + 2, "mark", [2]uint64{8, 9}})
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("effect-swaps %v, want %v", got, want)
	}
}
