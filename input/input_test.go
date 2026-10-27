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
	for _, want := range []string{`has the keys "action", "obs"`, `{"protocol": "gotogether.requests/1", "recorder": ...}`, `"quint.action"`, `"quint.obs."<name>`, "qobs.JSONLSink", StepLogExample} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	// the example it gives is a step log, of the step it says
	if err := os.WriteFile(p, []byte(StepLogExample+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Read(p, "")
	if err != nil || len(f.Steps) != 1 {
		t.Fatalf("the example: %v %+v", err, f)
	}
	if s := f.Steps[0]; s.Action != "deposit" || s.Args["amount"] != int64(5) || s.Obs["balance"] != int64(5) {
		t.Errorf("the example reads as %+v", s)
	}
}

// A step's source names its file and line (file:N), so a report over
// several inputs says which file a line is in.
func TestStepSourceNamesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.jsonl")
	if err := os.WriteFile(p, []byte(StepLogExample+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Read(p, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Steps) != 1 || f.Steps[0].Source != p+":1" {
		t.Fatalf("steps %+v", f.Steps)
	}
}

// A step log cut off in its first line is torn, as one cut off later is,
// not a file of no known shape: a rerun killed while writing its first
// step was once told its log is "not OTLP/JSON, a step log or ITF".
func TestTornFirstLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "run.jsonl")
	if err := os.WriteFile(p, []byte(StepLogExample[:len(StepLogExample)/2]), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Read(p, "")
	if err != nil || f.Torn == nil || f.Torn.Line != 1 || len(f.Steps) != 0 || f.Format != StepLog {
		t.Fatalf("a torn first line: %v %+v", err, f)
	}
	// a whole object of no known shape on one line is still refused
	if err := os.WriteFile(p, []byte(`{"action":"reserve"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(p, ""); err == nil || !strings.Contains(err.Error(), "not OTLP/JSON, a step log, a request log or ITF") {
		t.Errorf("a whole object: %v", err)
	}
}

// A driver's request log is read by its header: its requests
// invocations, each completed by its own response, its steps naming the
// file's lines; a torn one keeps what came before the cut.
func TestRequestLog(t *testing.T) {
	p := filepath.Join(t.TempDir(), "requests.jsonl")
	log := `{"protocol":"gotogether.requests/1","recorder":"ui"}
{"event":"request","req":"1","action":"deposit","time":"2026-10-27T09:00:00Z","args":{"amount":5}}
{"event":"response","req":"1","time":"2026-10-27T09:00:01Z","code":200,"args":{"balance":5}}
`
	if err := os.WriteFile(p, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Read(p, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.Format != Requests || len(f.Steps) != 2 || !f.Steps[0].Invocation || f.Steps[1].Call != 1 || f.Steps[1].Source != p+":3" ||
		f.Steps[1].Args["balance"] != int64(5) || f.Steps[1].Args["code"] != int64(200) {
		t.Fatalf("%+v", f)
	}
	if err := os.WriteFile(p, []byte(log[:len(log)-20]), 0o644); err != nil {
		t.Fatal(err)
	}
	if f, err = Read(p, ""); err != nil || f.Torn == nil || f.Torn.Line != 3 || len(f.Steps) != 2 || f.Steps[1].Lost != "suffix" {
		t.Fatalf("torn: %v %+v", err, f)
	}
	// a later version is refused with the fix, not read as another format
	if err := os.WriteFile(p, []byte(`{"protocol":"gotogether.requests/2","recorder":"ui"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(p, ""); err == nil || !strings.Contains(err.Error(), "upgrade gotogether") {
		t.Errorf("a later version: %v", err)
	}
}
