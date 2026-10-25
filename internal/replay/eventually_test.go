package replay

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
)

// An eventual val is expected once, after the last step: holding before
// the end (or never mid-run) is not what it promises.
func TestEventuallyExpectsTheLastState(t *testing.T) {
	dir := t.TempDir()
	ms := ModuleSpec{Spec: filepath.Join(dir, "m.qnt"), Module: "m", Eventually: []string{"done"}}
	calls := []binding.Call{{Quint: "a(1)", Action: "a"}, {Quint: "b(2)", Action: "b"}}
	g, err := RenderModule(ms, calls, filepath.Join(dir, "replay.qnt"))
	if err != nil {
		t.Fatal(err)
	}
	name := g.Tests.Eventually["done"]
	if name == "" {
		t.Fatalf("no eventual run for done: %+v", g.Tests)
	}
	run := g.Text[strings.Index(g.Text, "run "+name):]
	if end := strings.Index(run, "\n\n"); end > 0 {
		run = run[:end]
	}
	if strings.Count(run, ".expect(done)") != 1 || !strings.Contains(run, "b(2).expect(done)") {
		t.Errorf("done is not expected after the last step only:\n%s", run)
	}

	g, err = RenderModule(ms, nil, filepath.Join(dir, "replay.qnt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Text, "init.expect(done)") {
		t.Errorf("an empty run does not expect done at init:\n%s", g.Text)
	}
}
