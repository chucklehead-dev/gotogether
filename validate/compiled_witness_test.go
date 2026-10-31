package validate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// An offline artifact cannot turn finite witness search into a confirmed replay.
// Both an admitted observation and an impossible one stay unjudged, with useful
// metadata; neither gets a sampled failure or an evaluator-only passing witness.
func TestCompiledModelChoicesRequireConfirmation(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	dir := t.TempDir()
	for _, name := range []string{"binding.yaml", "model.qnt"} {
		b, err := os.ReadFile(filepath.Join("testdata", "witness", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bindingPath := filepath.Join(dir, "binding.yaml")
	artifact, err := Compile(bindingPath, Options{})
	if err != nil {
		t.Fatal(err)
	}
	compiledPath := filepath.Join(dir, "model.json")
	if err := artifact.Write(compiledPath); err != nil {
		t.Fatal(err)
	}
	c, err := NewCompiledChecker(bindingPath, compiledPath, Options{Dir: t.TempDir(), ChoiceLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Options.Quint != noQuint {
		t.Fatal("compiled checker can invoke Quint")
	}
	for _, observed := range []int64{1, 99} {
		steps := []qtrace.Step{{Actor: "offline", Recorder: "capture", Seq: 1, Action: "pick", Obs: map[string]any{"phase": observed}}}
		rep, err := c.Check(t.Context(), steps)
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Actors) != 1 {
			t.Fatalf("actor missing: %+v", rep)
		}
		a := rep.Actors[0]
		if a.Conforms != NotEvaluated || a.Failure != nil || rep.OK() || rep.Verdict().Verdict != NotEvaluated {
			t.Fatalf("offline choice observation %d became a verdict: %s", observed, rep.Summary())
		}
		if a.Choices == nil || a.Choices.Status != "unsupported" || a.Choices.Confirmation != "not_performed" || a.Choices.Explored != 0 || len(a.Choices.Witness) != 0 || !strings.Contains(a.Choices.Reason, "compiled mode") {
			t.Fatalf("missing offline refusal scope: %+v", a.Choices)
		}
		if a.Module != "" || a.ITF != "" {
			t.Fatalf("offline refusal fabricated replay artifacts: %+v", a)
		}
		for _, check := range a.Invariants {
			if check.Verdict != NotEvaluated {
				t.Fatalf("offline invariant judged: %+v", check)
			}
		}
		b, err := json.Marshal(a.Doc())
		if err != nil || !strings.Contains(string(b), `"confirmation":"not_performed"`) {
			t.Fatalf("JSON hid confirmation boundary: %s %v", b, err)
		}
	}
	// Other actions in this same model remain usable offline. A disabled
	// deterministic action has a decided failure, not a model-wide refusal.
	rep, err := c.Check(t.Context(), []qtrace.Step{{Actor: "offline", Recorder: "capture", Seq: 1, Action: "reset", Obs: map[string]any{"phase": int64(0)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Actors) != 1 || rep.Actors[0].Conforms != Fail || rep.Actors[0].Choices != nil {
		t.Fatalf("choice-free call was refused because the model has other choices: %s", rep.Summary())
	}
}

func TestCompiledRejectsNegativeChoiceLimitBeforeReadingArtifact(t *testing.T) {
	_, err := NewCompiledChecker("not-present.yaml", "not-present.json", Options{ChoiceLimit: -1})
	if err == nil || !strings.Contains(err.Error(), "choice limit must be nonnegative") {
		t.Fatalf("negative bound: %v", err)
	}
}
