package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// validate's report of a rejected step names the nav command that opens
// the model before it, and that command opens it: the state before the
// step, its branches, the rejected action among the disabled with why.
func TestNavFromValidate(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	steps := []qtrace.Step{
		{Action: "admit", Actor: "l", Recorder: "l", Thread: "l", Seq: 1, Args: map[string]any{"client": "a"}},
		{Action: "admit", Actor: "l", Recorder: "l", Thread: "l", Seq: 2, Args: map[string]any{"client": "b"}},
		{Action: "admit", Actor: "l", Recorder: "l", Thread: "l", Seq: 3, Args: map[string]any{"client": "c"}},
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "run.jsonl")
	f, err := os.Create(log)
	if err != nil {
		t.Fatal(err)
	}
	if err := qtrace.WriteJSONL(f, steps); err != nil {
		t.Fatal(err)
	}
	f.Close()
	bind := "../../validate/testdata/limiter.yaml"
	code, out, err := validated([]string{"-binding", bind, "-dir", filepath.Join(dir, "out"), log})
	if err != nil || code != 1 {
		t.Fatalf("exit %d, %v:\n%s", code, err, out)
	}
	m := regexp.MustCompile(`navigate: gotogether nav (.*) -at (\d+) `).FindStringSubmatch(string(out))
	if m == nil || m[2] != "2" {
		t.Fatalf("no nav line at the rejected step:\n%s", out)
	}
	var buf bytes.Buffer
	if code, err := nav(&buf, []string{"-binding", bind, "-run", log, "-actor", "l", "-at", m[2], "-json"}); err != nil || code != 0 {
		t.Fatalf("nav: %d %v", code, err)
	}
	var node validate.Node
	if err := json.Unmarshal(buf.Bytes(), &node); err != nil {
		t.Fatalf("%v: %s", err, buf.String())
	}
	if len(node.State) != 1 || node.State[0].Value != `Set("a", "b")` || len(node.Disabled) != 1 || node.Disabled[0].Action != "admit" ||
		!strings.Contains(node.Disabled[0].Why.String(""), "admitted.size() < LIMIT") {
		t.Errorf("the node before the rejected step: %s", buf.String())
	}
	// a path from it
	buf.Reset()
	if code, err := nav(&buf, []string{"-binding", bind, "-run", log, "-at", "2", "-take", `release("a")`}); err != nil || code != 0 {
		t.Fatalf("nav -take: %d %v", code, err)
	}
	if !strings.Contains(buf.String(), `admitted = Set("b")`) {
		t.Errorf("after release(\"a\"):\n%s", buf.String())
	}
}
