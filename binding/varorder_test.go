package binding

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// varOrderShift prepends a module to testdata/varorder.qnt: its
// declarations take ids, which reorders the variables quint typechecks
// within a layer ([v u z y x w] becomes [w v u z y x]). It writes the
// copy into dir under the same name, so replays of both import alike.
func varOrderShift(t *testing.T, dir string) string {
	t.Helper()
	text, err := os.ReadFile("testdata/varorder.qnt")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "varorder.qnt")
	if err := os.WriteFile(path, append([]byte("module pad {\n  val p1 = 1\n}\n"), text...), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A model's variables are sorted by name, not in quint's order, which
// within a toposort layer follows its ids: a module written before the
// model leaves them, and the lint lists that follow them, as they were.
func TestVarsSorted(t *testing.T) {
	skip.Quint(t)
	want := []string{"u", "v", "w", "x", "y", "z"}
	for name, spec := range map[string]string{"plain": "testdata/varorder.qnt", "shifted": varOrderShift(t, t.TempDir())} {
		t.Run(name, func(t *testing.T) {
			m, err := LoadModel(t.Context(), "quint", spec, "order")
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(m.Vars, want) {
				t.Errorf("vars %v: want them by name, %v", m.Vars, want)
			}
			if got, err := m.CheckpointVars("restore"); err != nil || !slices.Equal(got, want) {
				t.Errorf("checkpoint vars %v (%v): want %v", got, err, want)
			}
			for _, c := range []struct {
				b    Binding
				want string
			}{
				{Binding{Snapshot: "onlyZ"}, "snapshot onlyZ leaves u, v, w, x, y unassigned: a snapshot assigns every state variable from its parameters (u' = ...)"},
				{Binding{Resume: "resumeZ"}, "resume resumeZ leaves u, v, w, x, y unassigned: a resume assigns every state variable (u' = ...)"},
			} {
				if got := strings.Join(c.b.Lint(m), "\n"); !strings.Contains(got, c.want) {
					t.Errorf("lint %q, want %q", got, c.want)
				}
			}
		})
	}
}
