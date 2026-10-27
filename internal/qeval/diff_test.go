package qeval

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
)

// Every model of the repository's bindings, in its binding's instance,
// against quint: each state of traces `quint run` makes is one of the
// evaluator's successors of the state before it, by the model's own step
// (init for the first). A model the evaluator refuses is reported, not
// failed; one it disagrees on fails, naming the state.
func TestAgreesWithQuint(t *testing.T) {
	skip.Quint(t)
	if testing.Short() {
		t.Skip("runs quint on every model of the repository")
	}
	var paths []string
	for _, g := range []string{"../../examples/*/*.binding.yaml", "../../examples/*/*/*.binding.yaml", "../../*/testdata/*.yaml", "../../examples/*/*binding*.yaml"} {
		ps, _ := filepath.Glob(g)
		paths = append(paths, ps...)
	}
	sort.Strings(paths)
	seen := map[string]bool{}
	checked := 0
	for _, p := range paths {
		b, err := binding.Load(p)
		if err != nil {
			continue // not a binding (a convert rule, a corpus)
		}
		key := b.SpecPath() + "|" + fmt.Sprint(b.Constants)
		if seen[key] || templated(b.Constants) {
			continue
		}
		seen[key] = true
		t.Run(strings.TrimPrefix(p, "../../"), func(t *testing.T) {
			if agree(t, b) {
				checked++
			}
		})
	}
	if checked < 10 {
		t.Errorf("only %d models checked: the evaluator refuses most of the repository's", checked)
	}
	t.Logf("%d models agree with quint", checked)
}

// templated reports whether a binding's constants come from its steps
// (set, max, count): no instance without a history.
func templated(cs map[string]string) bool {
	for _, v := range cs {
		if strings.Contains(v, "{{") {
			return true
		}
	}
	return false
}

// agree runs quint on b's model and checks the evaluator's successors
// against its traces; false if the evaluator refused the model.
func agree(t *testing.T, b *binding.Binding) bool {
	dir := t.TempDir()
	spec, _ := filepath.Abs(b.SpecPath())
	rel, err := filepath.Rel(dir, spec)
	if err != nil {
		t.Fatal(err)
	}
	rel = strings.TrimSuffix(rel, ".qnt")
	var cs []string
	for _, k := range slices.Sorted(maps.Keys(b.Constants)) {
		cs = append(cs, k+" = "+b.Constants[k])
	}
	imp := fmt.Sprintf("import %s.* from %q", b.Module, rel)
	if len(cs) > 0 {
		imp = fmt.Sprintf("import %s(%s).* from %q", b.Module, strings.Join(cs, ", "), rel)
	}
	mod := filepath.Join(dir, "diff.qnt")
	if err := os.WriteFile(mod, []byte("module diff {\n  "+imp+"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	irPath := filepath.Join(dir, "ir.json")
	f, _ := os.Create(irPath)
	cmd := exec.Command("quint", "compile", "--target", "json", "--main", "diff", mod)
	var stderr strings.Builder
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, f, &stderr
	err = cmd.Run()
	f.Close()
	if err != nil {
		t.Skipf("quint compile: %v %s", err, stderr.String()) // a model with no step: replay-only
	}
	data, _ := os.ReadFile(irPath)
	m, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	run := exec.Command("quint", "run", "--main", "diff", "--mbt", "--max-samples", "4", "--n-traces", "4", "--max-steps", "15",
		"--seed", "7", "--out-itf", filepath.Join(dir, "t_{seq}.itf.json"), mod)
	run.Dir = dir
	if b, err := run.CombinedOutput(); err != nil && !strings.Contains(string(b), "violation") {
		t.Skipf("quint run: %v\n%s", err, b)
	}
	traces, _ := filepath.Glob(filepath.Join(dir, "t_*.itf.json"))
	if len(traces) == 0 {
		t.Skip("quint run wrote no trace")
	}
	for _, tp := range traces {
		tr, err := itf.ReadFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		var prev State
		for i, st := range tr.States {
			want, err := stateOf(m, st)
			if err != nil {
				t.Logf("%s: %v", filepath.Base(tp), err)
				return false
			}
			action := "step"
			if i == 0 {
				action = "init"
			}
			succ, err := m.Successors(nil, prev, action)
			if err != nil {
				var u *Unsupported
				if asUnsupported(err, &u) {
					// every model of the repository is in the fragment: a
					// refusal is a regression (or a model that left it)
					t.Errorf("refused: %v", err)
					return false
				}
				t.Fatalf("%s state %d: %v", filepath.Base(tp), i, err)
			}
			found := false
			for _, s := range succ {
				if sameState(s, want) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s state %d (%s): quint's state is none of the evaluator's %d successors\n  quint: %s\n  first: %s",
					filepath.Base(tp), i, st.Action, len(succ), showState(want), showFirst(succ))
			}
			prev = want
		}
	}
	return true
}

func asUnsupported(err error, u **Unsupported) bool {
	x, ok := err.(*Unsupported)
	if ok {
		*u = x
	}
	return ok
}

// stateOf is an ITF state as the model's State, its variables matched to
// the IR's by their last segment.
func stateOf(m *Model, st itf.State) (State, error) {
	out := State{}
	for _, v := range m.Vars {
		short := v[strings.LastIndex(v, ":")+1:]
		x, ok := st.Vars[short]
		if !ok {
			return nil, fmt.Errorf("the trace has no %s", short)
		}
		val, err := FromITF(x)
		if err != nil {
			return nil, err
		}
		out[v] = val
	}
	return out, nil
}

func showState(s State) string {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(s)) {
		fmt.Fprintf(&b, "%s=%s ", k, String(s[k]))
	}
	return b.String()
}

func showFirst(ss []State) string {
	if len(ss) == 0 {
		return "(disabled)"
	}
	return showState(ss[0])
}
