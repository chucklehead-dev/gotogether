package binding

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A template splits into its text and fields; one with more than text
// and fields does not.
func TestSplitTemplate(t *testing.T) {
	parts, holes, err := splitTemplate(`result.get({{.id}}) == {{.obs.ok}} and s == {{toSet .ids}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(parts, []string{"result.get(", ") == ", " and s == ", ""}) || !slices.Equal(holes, []string{"{{.id}}", "{{.obs.ok}}", "{{toSet .ids}}"}) {
		t.Errorf("parts %q, holes %q", parts, holes)
	}
	// a field the text reads as part of a token means something else as a
	// parameter: inside a string, glued to a name or a number
	for _, bad := range []string{`{{if .x}}a{{end}}`, `{{range .xs}}{{.}}{{end}}`, `{{$v := .x}}{{$v}}`,
		`name == "n{{.id}}"`, `x == n{{.id}}`, `x == {{.id}}0`, `r.{{.f}} == 1`} {
		if _, _, err := splitTemplate(bad); err == nil {
			t.Errorf("%s split", bad)
		}
	}
	// applied to (a method, an index) it is the same Quint, but for a
	// negative value: -3^2 is -(3^2), a parameter -3 squared is 9
	if _, _, err := splitTemplate(`{{toList .xs}}.indices() == {{.n}}`); err != nil {
		t.Errorf("a field applied to: %v", err)
	}
	if atomic("-3", "^2") || !atomic("3", "^2") || !atomic("-3", " == x") {
		t.Errorf("atomic: a negative value before ^ reads otherwise")
	}
}

// A call's expect is its template with each field replaced by the call's
// ExpectArgs: the definition an EvalModule makes of it, applied to them,
// is the same Quint.
func TestExpectArgsRebuildTheExpect(t *testing.T) {
	b := &Binding{Spec: "m.qnt", Module: "m", Actions: map[string]Action{
		"transfer": {Expect: `result.get({{.id}}) == {{.obs.ok}}`},
		"read":     {Expect: `bal.get({{.account}}) == {{.obs.balance}} and {{toSet .seen}} == seen`},
	}}
	tr := &qtrace.Trace{Steps: []qtrace.Step{
		{Action: "transfer", Args: map[string]any{"id": int64(7)}, Obs: map[string]any{"ok": true}},
		{Action: "read", Args: map[string]any{"account": "a1", "seen": `["x","y"]`}, Obs: map[string]any{"balance": int64(-3)}},
	}}
	p, err := b.Plan(nil, tr)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Calls {
		parts, _, _ := splitTemplate(c.ExpectTemplate)
		var rebuilt strings.Builder
		for i, a := range c.ExpectArgs {
			rebuilt.WriteString(parts[i] + a)
		}
		rebuilt.WriteString(parts[len(c.ExpectArgs)])
		if len(c.ExpectArgs) != len(parts)-1 || rebuilt.String() != c.Expect {
			t.Errorf("%s: rebuilt %q, rendered %q", c.Action, rebuilt.String(), c.Expect)
		}
	}
}

// Every binding of the repository has an evaluation module quint
// compiles, its expects, vals and action wrappers in the compiled IR.
func TestEvalModuleCompiles(t *testing.T) {
	skip.Quint(t)
	if testing.Short() {
		t.Skip("compiles every binding's module with quint")
	}
	var paths []string
	for _, g := range []string{"../examples/*/*.binding.yaml", "../examples/*/*/*.binding.yaml", "../*/testdata/*.yaml"} {
		ps, _ := filepath.Glob(g)
		paths = append(paths, ps...)
	}
	n := 0
	for _, p := range paths {
		b, err := Load(p)
		if err != nil {
			continue
		}
		t.Run(strings.TrimPrefix(p, "../"), func(t *testing.T) {
			m, err := LoadModel("", b.SpecPath(), b.Module)
			if err != nil {
				t.Skipf("model: %v", err)
			}
			dir := t.TempDir()
			qnt := filepath.Join(dir, "eval.qnt")
			em, err := b.EvalModule(m, qnt)
			if err != nil {
				t.Errorf("no evaluation module: %v", err) // every binding of the repository has one
			}
			if err := os.WriteFile(qnt, []byte(em.Text), 0o644); err != nil {
				t.Fatal(err)
			}
			ir := filepath.Join(dir, "ir.json")
			f, _ := os.Create(ir)
			cmd := exec.Command("quint", "compile", "--target", "json", "--main", em.Name, "--init", em.Init, "--step", em.Init, qnt)
			var stderr strings.Builder
			cmd.Dir, cmd.Stdout, cmd.Stderr = dir, f, &stderr
			err = cmd.Run()
			f.Close()
			if err != nil {
				t.Fatalf("quint compile: %v\n%s\n%s", err, stderr.String(), em.Text)
			}
			data, _ := os.ReadFile(ir)
			for _, name := range append(append(mapValues(em.Expects), mapValues(em.Actions)...), append(mapValues(em.Invariants), mapValues(em.Eventually)...)...) {
				if !strings.Contains(string(data), `"name":"`+name+`"`) {
					t.Errorf("%s is not in the compiled IR", name)
				}
			}
			n++
		})
	}
	if n < 15 {
		t.Errorf("%d bindings' modules compiled", n)
	}
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
