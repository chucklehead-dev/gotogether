package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Hidden and resumed replay enumerate states instead of taking walk's single
// successor. A checkpoint must constrain each candidate before they are merged;
// otherwise restore can erase the preceding recorded write in either search.
func TestSnapshotCheckpointConstrainsSearchedStates(t *testing.T) {
	skip.Quint(t)
	for _, search := range []string{"hidden", "resume"} {
		t.Run(search, func(t *testing.T) {
			dir := t.TempDir()
			model := `module checkpoint {
  var x: int
  action init = x' = 0
  action write(v: int): bool = x' = v
  action restore(v: int): bool = x' = v
  action idle = x' = x
  action resumeAny = { nondet v = Set(0, 1).oneOf() x' = v }
  action step = { nondet v = Set(0, 1, 2).oneOf() write(v) }
}
`
			if err := os.WriteFile(filepath.Join(dir, "checkpoint.qnt"), []byte(model), 0600); err != nil {
				t.Fatal(err)
			}
			binding := "spec: checkpoint.qnt\nmodule: checkpoint\nsnapshot: restore\nactions:\n  write: {}\n  restore: {}\n"
			first := uint64(1)
			if search == "hidden" {
				binding += "hidden: [idle]\n"
			} else {
				binding += "resume: resumeAny\n"
				first = 3 // missing seqs 1..2 requires the admitted-state search
			}
			path := filepath.Join(dir, "checkpoint.yaml")
			if err := os.WriteFile(path, []byte(binding), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := NewChecker(path, Options{Dir: filepath.Join(dir, "out")})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { c.Close() })
			for _, value := range []int64{1, 2} {
				run := []qtrace.Step{
					{Action: "write", Recorder: "r", Seq: first, Args: map[string]any{"v": int64(1)}},
					{Action: "restore", Recorder: "r", Seq: first + 1, Source: "checkpoint.jsonl:2", Args: map[string]any{"v": value}},
				}
				r, err := c.Check(t.Context(), run)
				if err != nil {
					t.Fatal(err)
				}
				if len(r.Actors) != 1 {
					t.Fatalf("snapshot %d: actors %d, want one", value, len(r.Actors))
				}
				a := r.Actors[0]
				if value == 1 {
					if !r.OK() || a.Conforms != Pass {
						t.Fatalf("matching searched checkpoint:\n%s", r.Summary())
					}
				} else if r.OK() || a.Conforms != Fail || a.Failure == nil || a.Failure.Kind != "state" || a.Failure.Step == nil || a.Failure.Step.Source != "checkpoint.jsonl:2" {
					t.Fatalf("checkpoint reset the searched state or lost its identity:\n%s", r.Summary())
				}
			}
		})
	}
}
