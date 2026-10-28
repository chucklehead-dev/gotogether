package validate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// compiledCopy copies the binding at testdata/<name>.yaml and its spec
// into a directory of the test's, compiles it there, and returns the
// binding's and the compiled model's paths.
func compiledCopy(t *testing.T, name, spec string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{name + ".yaml", spec} {
		b, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bp, out := filepath.Join(dir, name+".yaml"), filepath.Join(dir, name+".model.json")
	cm, err := Compile(bp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.Write(out); err != nil {
		t.Fatal(err)
	}
	return bp, out
}

func compiledSteps(ops ...any) []qtrace.Step {
	sink := &qobs.MemorySink{}
	r := qobs.NewRecorder(sink, qobs.WithActor("acct-1"))
	for i := 0; i < len(ops); i += 3 {
		r.Record(context.Background(), ops[i].(string), nil, "amount", ops[i+1], "obs.balance", ops[i+2])
	}
	return sink.Steps()
}

// A compiled model judges as quint does, with no quint: a pass, a step
// the model cannot take, an expect false after a step, an invariant
// violated, a bounded response late, never and in time, each with
// quint's reason, failing step and kind. Every result says the evaluator
// alone judged it.
func TestCompiledAgreesWithQuint(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	timed := func(steps ...any) []qtrace.Step {
		var out []qtrace.Step
		for i := 0; i < len(steps); i += 2 {
			out = append(out, qtrace.Step{Action: steps[i].(string), Recorder: "n", Seq: uint64(i/2 + 1), Time: time.Unix(0, 0).Add(time.Duration(steps[i+1].(int)) * time.Second)})
		}
		return out
	}
	settled := func(h []qtrace.Step) []qtrace.Step {
		last := h[len(h)-1]
		return append(h, qtrace.Step{Action: "settled", Settled: true, Recorder: "n", Seq: last.Seq + 1, Time: last.Time})
	}
	for _, m := range []struct {
		binding, spec string
		runs          map[string][]qtrace.Step
	}{
		{"account", "account.qnt", map[string][]qtrace.Step{
			"conforms":      compiledSteps("deposit", 50, 50, "withdraw", 20, 30, "deposit", 5, 35),
			"overdraw":      compiledSteps("deposit", 10, 10, "withdraw", 20, -10),
			"wrong balance": compiledSteps("deposit", 10, 10, "deposit", 5, 16),
			"over limit":    compiledSteps("deposit", 60, 60, "deposit", 60, 120, "withdraw", 1, 119),
		}},
		{"within", "within.qnt", map[string][]qtrace.Step{
			"in time":        timed("crash", 0, "heal", 1, "elect", 2),
			"late":           timed("crash", 0, "heal", 1, "elect", 5),
			"cut short":      timed("crash", 0, "heal", 1, "tick", 2),
			"never, settled": settled(timed("crash", 0, "heal", 1, "tick", 2)),
		}},
	} {
		bp, cp := compiledCopy(t, m.binding, m.spec)
		q, err := NewChecker(bp, Options{Dir: t.TempDir(), Eval: "quint"})
		if err != nil {
			t.Fatal(err)
		}
		c, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if c.Options.Quint != noQuint || c.Compiled() == nil {
			t.Fatalf("a compiled checker's quint is %q: it must run none", c.Options.Quint)
		}
		for name, steps := range m.runs {
			name = m.binding + ": " + name
			want, err := q.Check(context.Background(), steps)
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.Check(context.Background(), steps)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			w, g := want.Actors[0], got.Actors[0]
			if w.Reason() != g.Reason() || w.OK() != g.OK() {
				t.Errorf("%s: compiled %q (OK %v), quint %q (OK %v)\n%s", name, g.Reason(), g.OK(), w.Reason(), w.OK(), got.Summary())
			}
			if g.JudgedBy() != "evaluator" || !strings.Contains(got.Summary(), "judged by the Go evaluator alone") {
				t.Errorf("%s: judged by %s, the report not saying the evaluator alone judged:\n%s", name, g.JudgedBy(), got.Summary())
			}
			wf, gf := w.failures(), g.failures()
			for i, f := range wf {
				if i >= len(gf) || gf[i].Kind != f.Kind || gf[i].Index != f.Index || gf[i].Call != f.Call {
					t.Errorf("%s: failures %+v, quint's %+v", name, gf, wf)
					break
				}
			}
		}
	}
}

// What the evaluator cannot decide is no verdict, never a guess: a fold
// whose result depends on quint's order of a set (review 11's F1, by
// default quint's Mismatch), and a search, whose orders quint confirms.
func TestCompiledUnsupportedIsNoVerdict(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	bp, cp := compiledCopy(t, "rr", "rr.qnt")
	c, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, s2 := range []int64{0, 1} {
		steps := []qtrace.Step{{Action: "place", Recorder: "c", Seq: 1, Time: time.Unix(1, 0), Obs: map[string]any{"s2": s2}}}
		rep, err := c.Check(context.Background(), steps)
		if err != nil {
			t.Fatal(err)
		}
		a := rep.Actors[0]
		if a.Reason() != "no verdict (needs quint)" || rep.Verdict().Verdict != NotEvaluated {
			t.Errorf("s2=%d: %q, want no verdict (needs quint):\n%s", s2, a.Reason(), rep.Summary())
		}
		if !strings.Contains(rep.Summary(), "validate it without -compiled") {
			t.Errorf("s2=%d: the fix unnamed:\n%s", s2, rep.Summary())
		}
	}
	steps := compiledSteps("deposit", 1, 1)
	ac, acp := compiledCopy(t, "account", "account.qnt")
	a, err := NewCompiledChecker(ac, acp, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Judge(context.Background(), steps, SearchOptions{Linearize: true}); !errors.Is(err, ErrNeedsQuint) {
		t.Errorf("a search by a compiled model: %v, want ErrNeedsQuint", err)
	}
}

// A stale compiled model is refused, naming what changed and the fix: a
// binding edited since, a source changed, an evaluation module this
// gotogether writes otherwise, a later format, a quint the evaluator does
// not read. An absent source is judged by the artifact's hash, and the
// report says so.
func TestCompiledStaleRefused(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	steps := compiledSteps("deposit", 10, 10)
	edit := func(t *testing.T, path string, f func([]byte) []byte) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, f(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	editModel := func(t *testing.T, cp string, f func(m map[string]any)) {
		edit(t, cp, func(b []byte) []byte {
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			f(m)
			out, _ := json.Marshal(m)
			return out
		})
	}
	for _, c := range []struct {
		name, want string
		change     func(t *testing.T, bp, cp string)
	}{
		{"binding", "compiled from another binding", func(t *testing.T, bp, _ string) {
			edit(t, bp, func(b []byte) []byte { return append(b, []byte("\n# edited\n")...) })
		}},
		{"source", "account.qnt changed since it was compiled", func(t *testing.T, bp, _ string) {
			edit(t, filepath.Join(filepath.Dir(bp), "account.qnt"), func(b []byte) []byte { return append(b, []byte("\n// edited\n")...) })
		}},
		{"evaluation module", "writes the binding's evaluation module otherwise", func(t *testing.T, _, cp string) {
			editModel(t, cp, func(m map[string]any) { m["eval"].(map[string]any)["Text"] = "module other {}" })
		}},
		{"format", "gotogether.model/2 is a later version", func(t *testing.T, _, cp string) {
			editModel(t, cp, func(m map[string]any) { m["format"] = "gotogether.model/2" })
		}},
		{"quint", "older than the 0.32", func(t *testing.T, _, cp string) {
			editModel(t, cp, func(m map[string]any) { m["quint"] = "0.31.0" })
		}},
		{"nothing", "", func(*testing.T, string, string) {}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			bp, cp := compiledCopy(t, "account", "account.qnt")
			c.change(t, bp, cp)
			ch, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("an artifact just compiled refused: %v", err)
			case c.want == "":
				rep, err := ch.Check(context.Background(), steps)
				if err != nil || !rep.OK() {
					t.Fatalf("%v\n%s", err, rep.Summary())
				}
			case err == nil:
				t.Fatalf("a stale artifact (%s) accepted", c.name)
			case !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "compile"):
				t.Errorf("refused with %q, want %q and the fix", err, c.want)
			}
		})
	}
	t.Run("absent source", func(t *testing.T) {
		t.Parallel()
		bp, cp := compiledCopy(t, "account", "account.qnt")
		if err := os.Remove(filepath.Join(filepath.Dir(bp), "account.qnt")); err != nil {
			t.Fatal(err)
		}
		ch, err := NewCompiledChecker(bp, cp, Options{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		rep, err := ch.Check(context.Background(), steps)
		if err != nil || !rep.OK() {
			t.Fatalf("%v\n%s", err, rep.Summary())
		}
		if !strings.Contains(rep.Summary(), "account.qnt is not here") {
			t.Errorf("an unchecked source unsaid:\n%s", rep.Summary())
		}
	})
}
