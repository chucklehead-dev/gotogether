package qtrace

import (
	"bytes"
	"reflect"
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
