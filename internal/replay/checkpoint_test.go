package replay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// Eager capture is semantically necessary: a val can be reevaluated in the
// post-state and make the comparison tautological. Exercise both evaluators,
// every variable (including a generated-name collision), and nested values.
func TestCheckpointCapturesCompletePreState(t *testing.T) {
	skip.Quint(t)
	vars := []string{"amount", "ops", "items", "__gtg_checkpoint_before"}
	model := `module checkpoint {
 var amount: int
 var ops: int
 var items: str -> { n: int }
 var __gtg_checkpoint_before: int
 action init = all { amount' = 7, ops' = 2, items' = Map("x" -> { n: 3 }), __gtg_checkpoint_before' = 4 }
 action restore(a: int, n: int, item_: int, hidden_: int): bool = all { amount' = a, sameRest(n, item_, hidden_) }
 action sameRest(n: int, item_: int, hidden_: int): bool = all { ops' = n, items' = Map("x" -> { n: item_ }), __gtg_checkpoint_before' = hidden_ }
`
	for _, backend := range []string{"typescript", "rust"} {
		for _, tc := range []struct {
			name, action string
			pass         bool
		}{
			{"matching", "restore(7, 2, 3, 4)", true},
			{"wrong_first", "restore(8, 2, 3, 4)", false},
			{"wrong_second", "restore(7, 8, 3, 4)", false},
			{"wrong_map_record", "restore(7, 2, 8, 4)", false},
			{"wrong_collision", "restore(7, 2, 3, 8)", false},
		} {
			t.Run(backend+"/"+tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "checkpoint.qnt")
				text := model + " run checkpointTest = init.then(" + checkpoint(tc.action, vars) + ")\n}\n"
				if err := os.WriteFile(path, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
				out, err := quintcmd.Command(t.Context(), "quint", filepath.Dir(path), "test", path, "--backend="+backend, "--max-samples=1").CombinedOutput()
				if tc.pass && err != nil {
					t.Fatalf("matching checkpoint: %v\n%s", err, out)
				}
				if !tc.pass && (err == nil || !strings.Contains(string(out), "QNT508")) {
					t.Fatalf("mismatched checkpoint needs semantic QNT508, got %v\n%s", err, out)
				}
			})
		}
	}
}

func TestCheckpointNeedsKnownState(t *testing.T) {
	_, err := RenderModule(ModuleSpec{Spec: "m.qnt", Module: "m"}, []binding.Call{{Checkpoint: true, Action: "restore", Quint: "restore(1)"}}, "replay.qnt")
	if err == nil || !strings.Contains(err.Error(), "complete state variables") {
		t.Fatalf("checkpoint without model state: %v", err)
	}
}
