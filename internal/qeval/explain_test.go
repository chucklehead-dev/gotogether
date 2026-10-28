package qeval

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// compileFile compiles a .qnt file's module main (init and step) to IR.
func compileFile(t testing.TB, path, main string) *Model {
	t.Helper()
	skip.Quint(t)
	abs, _ := filepath.Abs(path)
	out := filepath.Join(t.TempDir(), "ir.json")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("quint", "compile", "--target", "json", "--main", main, abs)
	var stderr strings.Builder
	cmd.Dir, cmd.Stdout, cmd.Stderr = filepath.Dir(abs), f, &stderr
	err = cmd.Run()
	f.Close()
	if err != nil {
		t.Fatalf("quint compile: %v\n%s", err, stderr.String())
	}
	data, _ := os.ReadFile(out)
	m, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func limiter(t *testing.T) (*Model, map[string]Value) {
	m := compileFile(t, "../../validate/testdata/limiter.qnt", "limiter")
	return m, map[string]Value{"LIMIT": Int(2), "CLIENTS": strs("a", "b", "c")}
}

func step(t *testing.T, m *Model, consts map[string]Value, s State, action string, args ...Value) State {
	t.Helper()
	next, ok, err := m.Step(consts, s, action, args...)
	if err != nil || !ok {
		t.Fatalf("%s%v: enabled %v, %v", action, args, ok, err)
	}
	return next
}

// A disabled step's explanation names the conjunct that is false and
// the values it compares: admit("c") with two admitted fails
// admitted.size() < LIMIT, 2 < 2, not its first conjunct.
func TestExplainNamesTheConjunct(t *testing.T) {
	m, consts := limiter(t)
	s, err := m.Init(consts, "init")
	if err != nil {
		t.Fatal(err)
	}
	s = step(t, m, consts, s, "admit", Str("a"))
	s = step(t, m, consts, s, "admit", Str("b"))
	w, err := m.Explain(consts, s, "", "admit", Str("c"))
	if err != nil || w == nil {
		t.Fatalf("explain: %v %v", w, err)
	}
	t.Logf("why:\n%s", w.String("  "))
	leaf := w.Leaf()
	if leaf.Expr != "admitted.size() < LIMIT" || leaf.In != "admit" {
		t.Fatalf("the false conjunct: %+v\n%s", leaf, w.String(""))
	}
	got := map[string]string{}
	for _, v := range leaf.Values {
		got[v.Name] = String(v.Value)
	}
	if got["admitted.size()"] != "2" || got["LIMIT"] != "2" || got["admitted"] != `Set("a", "b")` {
		t.Errorf("its values: %v", got)
	}
	// its first conjunct is the one false for a client admitted already
	w, err = m.Explain(consts, s, "", "admit", Str("a"))
	if err != nil || w.Leaf().Expr != `not(admitted.contains(client))` {
		t.Errorf("admit(a): %v\n%s", err, w.String(""))
	}
	// an enabled action has no explanation
	if w, err := m.Explain(consts, s, "", "release", Str("a")); w != nil || err != nil {
		t.Errorf("release(a) is enabled: %v %v", w, err)
	}
}

// Under a step's any {}, Explain's only keeps the branch of one action,
// through the nondet before it: why the step cannot admit.
func TestExplainBranch(t *testing.T) {
	m, consts := limiter(t)
	s, _ := m.Init(consts, "init")
	s = step(t, m, consts, s, "admit", Str("a"))
	s = step(t, m, consts, s, "admit", Str("b"))
	w, err := m.Explain(consts, s, "admit", "step")
	if err != nil || w == nil {
		t.Fatalf("explain step's admit: %v %v", w, err)
	}
	t.Logf("why step cannot admit:\n%s", w.String("  "))
	if !strings.Contains(w.String(""), "admitted.size() < LIMIT") && !strings.Contains(w.String(""), "not(admitted.contains(client))") {
		t.Errorf("step's admit: %s", w.String(""))
	}
	// release is enabled for some client: nothing to explain of it as a
	// whole, but its branch for the first client (c, never admitted) is
	// disabled; the navigator asks only for actions no transition takes
	if as := m.BranchActions("step"); !slices.Equal(as, []string{"admit", "release"}) {
		t.Errorf("step's branch actions: %v", as)
	}
}

// Transitions label each successor with the action applied and the
// picks made, and agree with brute force: an action with given arguments
// is enabled exactly when some transition applies it to them, and the
// transitions reach exactly Successors' states.
func TestTransitionsAgainstBruteForce(t *testing.T) {
	m, consts := limiter(t)
	s, _ := m.Init(consts, "init")
	states := []State{s}
	for range 3 { // every state within 3 steps
		var next []State
		for _, s := range states {
			ts, err := m.Transitions(consts, s, "step")
			if err != nil {
				t.Fatal(err)
			}
			labelled := map[string]bool{}
			for _, tr := range ts {
				as := tr.Actions()
				if len(as) == 0 || len(tr.Picks()) != 1 || tr.Picks()[0].Pick != "c" {
					t.Fatalf("a transition's label: %v", tr.Events)
				}
				labelled[as[0].String()] = true
				next = append(next, tr.State)
			}
			for _, a := range []string{"admit", "release"} {
				for _, c := range []string{"a", "b", "c"} {
					succ, err := m.Successors(consts, s, a, Str(c))
					if err != nil {
						t.Fatal(err)
					}
					label := a + `(client = "` + c + `")`
					if (len(succ) > 0) != labelled[label] {
						t.Errorf("in %s: %s enabled %v, labelled %v", showState(s), label, len(succ) > 0, labelled[label])
					}
				}
			}
			succ, _ := m.Successors(consts, s, "step")
			keys := map[string]bool{}
			for _, tr := range ts {
				keys[m.Key(tr.State)] = true
			}
			if len(keys) != len(succ) {
				t.Errorf("in %s: transitions reach %d states, Successors %d", showState(s), len(keys), len(succ))
			}
		}
		states = next
	}
}
