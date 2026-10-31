package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A recorded observation can identify a later initializer alternative. The
// CLI's manifest must replay that whole-path choice and retain its evidence.
func TestInitialChoicesManifestReplay(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	for name, data := range map[string]string{
		"model.qnt": `module initialReplay {
  var leader: int
  action init = { nondet chosen = Set(1, 2).oneOf(); leader' = chosen }
  action read = leader' = leader
  val safe = leader >= 1 and leader <= 2
}`,
		"binding.yaml": "spec: model.qnt\nmodule: initialReplay\nactions:\n  read:\n    expect: 'leader == {{.obs.leader}}'\ninvariants: [safe]\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := filepath.Join(dir, "run.jsonl")
	f, err := os.Create(run)
	if err != nil {
		t.Fatal(err)
	}
	err = qtrace.WriteJSONL(f, []qtrace.Step{{Actor: "cluster", Recorder: "capture", Seq: 1, Action: "read", Obs: map[string]any{"leader": int64(2)}}})
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("write run: %v, close: %v", err, closeErr)
	}
	j, err := (Check{Binding: filepath.Join(dir, "binding.yaml"), Files: []string{run}, Options: validate.Options{Eval: "go", ChoiceLimit: 8}}).Judge(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if !j.OK || j.Judgment == nil || len(j.Judgment.Report.Actors) != 1 {
		t.Fatalf("initial-choice observation did not pass: %+v", j.Verdict)
	}
	a := j.Judgment.Report.Actors[0]
	if a.Choices == nil || a.Choices.Initialization == nil || a.Choices.Initialization.Action != "init" || a.Choices.Initialization.Expression == "" || a.Choices.Confirmation != "path_confirmed" {
		t.Fatalf("initializer evidence missing: %+v", a.Choices)
	}
	encoded, err := json.Marshal(a.Doc())
	if err != nil || !strings.Contains(string(encoded), `"initialization":{"action":"init","expression":`) {
		t.Fatalf("agent JSON lost initializer: %s, %v", encoded, err)
	}
	m, err := j.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := m.Write(path); err != nil {
		t.Fatal(err)
	}
	m, err = validate.ReadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := Replay(t.Context(), m, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer replayed.Got.Close()
	if !replayed.Same || !replayed.Got.OK {
		t.Fatalf("initial-choice replay differs: %s", replayed.Differs)
	}
	got := replayed.Got.Judgment.Report.Actors[0].Choices
	if got == nil || got.Initialization == nil || *got.Initialization != *a.Choices.Initialization || got.Limit != 8 {
		t.Fatalf("replay changed initializer or limit: %+v", got)
	}
}
