package connect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/itf"
	"golang.org/x/mod/semver"
)

// Eval reads quint 0.34's named vals (`invariant x = e`, `observation
// x = e`) as it reads a val.
func TestEvalNamedVals(t *testing.T) {
	needQuint(t)
	if v, err := quintcmd.Version(t.Context(), "quint", ""); err != nil || semver.Compare("v"+v, "v0.34.0") < 0 {
		t.Skipf("quint %s (%v) parses no invariant or observation definitions: 0.34 added them", v, err)
	}
	dir := t.TempDir()
	spec := filepath.Join(dir, "named.qnt")
	if err := os.WriteFile(spec, []byte("module named {\n  var n: int\n  action init = n' = 0\n  action step = n' = n + 1\n  invariant nonNegative = n >= 0\n  observation two = n == 2\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ev := &evaluator{ctx: t.Context(), c: Config{Spec: spec, Dir: dir}}
	s := &Step{Index: 1, Action: "step", After: map[string]itf.Value{"n": intValue(2)}, eval: ev}
	for _, name := range []string{"nonNegative", "two"} {
		if v, err := Eval[bool](s, name); err != nil || !v {
			t.Errorf("%s at n = 2: %v, %v; want true", name, v, err)
		}
	}
}
