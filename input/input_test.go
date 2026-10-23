package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file in no known shape is refused with the keys it has and the shapes
// wanted, a step log's spelled out: "not OTLP/JSON, a step log or ITF" once
// left the user to guess.
func TestUnknownShapeSaysWhatIsWanted(t *testing.T) {
	p := filepath.Join(t.TempDir(), "run.jsonl")
	if err := os.WriteFile(p, []byte(`{"action":"reserve","obs":{"left":2}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(p, "")
	for _, want := range []string{`has the keys "action", "obs"`, `"quint.action"`, `"obs.<name>"`, "qobs.JSONLSink"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

// A step's source names its file, so a report over several inputs says
// which file a line is in.
func TestStepSourceNamesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.jsonl")
	if err := os.WriteFile(p, []byte(`{"quint.action":"deposit","quint.seq":1,"quint.recorder":"r","amount":5}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Read(p, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Steps) != 1 || f.Steps[0].Source != "a.jsonl line 1" {
		t.Fatalf("steps %+v", f.Steps)
	}
}
