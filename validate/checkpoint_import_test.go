package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Imported state is still state even when the bound module does not export its
// variable. Capturing only x let Quint accept a checkpoint that changed inner.y,
// while the native evaluator rejected it. Refuse incomplete capture consistently;
// exporting the whole state supports the checkpoint and finds its disagreement.
func TestCheckpointImportedState(t *testing.T) {
	skip.Quint(t)
	for _, scope := range []string{"private", "qualified", "exported"} {
		t.Run(scope, func(t *testing.T) {
			dir := t.TempDir()
			imp, helper := "import inner.*", "setY"
			if scope == "qualified" {
				imp, helper = "import inner", "inner::setY"
			}
			if scope == "exported" {
				imp += "\n export inner.*"
			}
			model := `module inner { var y: int
 action setY(v: int): bool = y' = v
}
module checkpoint {
 ` + imp + `
 var x: int
 action init = all { x' = 0, ` + helper + `(0) }
 action write(v: int): bool = all { x' = v, ` + helper + `(v) }
 action restore(a: int, b: int): bool = all { x' = a, ` + helper + `(b) }
 action step = { nondet v = Set(0,1,2).oneOf() write(v) }
}`
			if err := os.WriteFile(filepath.Join(dir, "checkpoint.qnt"), []byte(model), 0600); err != nil {
				t.Fatal(err)
			}
			bindingPath := filepath.Join(dir, "checkpoint.yaml")
			if err := os.WriteFile(bindingPath, []byte("spec: checkpoint.qnt\nmodule: checkpoint\nsnapshot: restore\nactions:\n  write:\n    args: { v: '{{.v}}' }\n  restore:\n    args: { a: '{{.a}}', b: '{{.b}}' }\n"), 0600); err != nil {
				t.Fatal(err)
			}
			steps := []qtrace.Step{
				{Action: "write", Recorder: "r", Seq: 1, Args: map[string]any{"v": int64(1)}},
				{Action: "restore", Recorder: "r", Seq: 2, Args: map[string]any{"a": int64(1), "b": int64(2)}},
			}
			for _, mode := range []string{"go", "quint", "shadow"} {
				t.Run(mode, func(t *testing.T) {
					c, err := NewChecker(bindingPath, Options{Dir: t.TempDir(), Eval: mode})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { c.Close() })
					rep, err := c.Check(t.Context(), steps)
					if scope == "exported" {
						if err != nil || rep.Actors[0].Conforms != Fail {
							t.Fatalf("exported y disagreement: %v, %+v", err, rep)
						}
					} else if err == nil || !strings.Contains(err.Error(), "complete imported state") {
						t.Fatalf("incomplete capture earned a verdict: %v, %+v", err, rep)
					}
					// An initial snapshot initializes state and needs no pre-state capture.
					head := steps[1]
					head.Seq = 1
					if rep, err := c.Check(t.Context(), []qtrace.Step{head}); err != nil || !rep.OK() {
						t.Fatalf("initial snapshot changed: %v, %+v", err, rep)
					}
					traces, _ := c.Binding.Reconstruct(steps)
					data, err := c.Binding.ObservedITF(traces[0])
					if err != nil {
						t.Fatal(err)
					}
					tr, err := itf.Parse(data)
					if err != nil {
						t.Fatal(err)
					}
					result, err := ValidateITF(t.Context(), tr, ITFOptions{Binding: c.Binding, NoVarCheck: true, Options: Options{Dir: t.TempDir()}})
					if scope == "exported" {
						if err != nil || result.Conforms != Fail {
							t.Fatalf("exported ITF y disagreement: %v, %+v", err, result)
						}
						matching := []qtrace.Step{steps[0], steps[1]}
						matching[1].Args = map[string]any{"a": int64(1), "b": int64(1)}
						if rep, err := c.Check(t.Context(), matching); err != nil || !rep.OK() {
							t.Fatalf("exported matching checkpoint: %v, %+v", err, rep)
						}
						traces, _ := c.Binding.Reconstruct(matching)
						data, err := c.Binding.ObservedITF(traces[0])
						if err != nil {
							t.Fatal(err)
						}
						tr, err := itf.Parse(data)
						if err != nil {
							t.Fatal(err)
						}
						if result, err := ValidateITF(t.Context(), tr, ITFOptions{Binding: c.Binding, NoVarCheck: true, Options: Options{Dir: t.TempDir()}}); err != nil || result.Conforms != Pass {
							t.Fatalf("exported matching ITF checkpoint: %v, %+v", err, result)
						}
					} else if err == nil || !strings.Contains(err.Error(), "complete imported state") {
						t.Fatalf("incomplete ITF capture earned a verdict: %v, %+v", err, result)
					}
				})
			}
		})
	}
}
