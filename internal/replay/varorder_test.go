package replay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// A checkpoint compares the state by fields in the variables' order: by
// name (binding.Model.Vars), so a module written before the model, whose
// ids reorder the variables quint typechecks, leaves the replay's text as
// it was.
func TestCheckpointTextIgnoresQuintOrder(t *testing.T) {
	skip.Quint(t)
	text, err := os.ReadFile("../../binding/testdata/varorder.qnt")
	if err != nil {
		t.Fatal(err)
	}
	b := &binding.Binding{Module: "order", Snapshot: "restore", Actions: map[string]binding.Action{"setZ": {}, "restore": {}}}
	tr := &qtrace.Trace{Processes: []string{"p"}, Steps: []qtrace.Step{
		{Action: "setZ", Seq: 1, Recorder: "r", Process: "p", Args: map[string]any{"a": int64(1)}},
		{Action: "restore", Seq: 2, Recorder: "r", Process: "p", Args: map[string]any{"a": int64(1), "b": int64(0), "c": int64(0), "d": int64(0), "e": int64(0), "f": int64(0)}},
	}}
	render := func(prefix string) string {
		dir := t.TempDir()
		spec := filepath.Join(dir, "varorder.qnt")
		if err := os.WriteFile(spec, append([]byte(prefix), text...), 0o600); err != nil {
			t.Fatal(err)
		}
		m, err := binding.LoadModel(t.Context(), "quint", spec, "order")
		if err != nil {
			t.Fatal(err)
		}
		b.Spec = spec
		g, err := Generate(b, m, tr, filepath.Join(dir, "replay.qnt"))
		if err != nil {
			t.Fatal(err)
		}
		return g.Text
	}
	plain, shifted := render(""), render("module pad {\n  val p1 = 1\n}\n")
	if plain != shifted {
		t.Errorf("a module written before the model changed the replay:\n%s\nshifted:\n%s", plain, shifted)
	}
	if want := "{ v0: u, v1: v, v2: w, v3: x, v4: y, v5: z }"; !strings.Contains(plain, want) {
		t.Errorf("replay without the checkpoint's state %s, by name:\n%s", want, plain)
	}
}
