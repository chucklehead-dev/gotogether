package binding

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"golang.org/x/mod/semver"
)

// namedValsSpec declares quint 0.34's named vals beside a plain one.
const namedValsSpec = `module named {
  var v: int
  action init = v' = 0
  action step = v' = v + 1
  val plain = v < 10
  invariant nonNegative = v >= 0
  observation reachedTwo: bool = v == 2
}
`

// quint 0.34's `invariant x = e` and `observation x = e` are vals that
// quint collects by name: a binding names them as any boolean val (they
// were "not a val" to the binding when quint 0.35 was pinned).
func TestNamedValsAreVals(t *testing.T) {
	skip.Quint(t)
	if v, err := quintcmd.Version(t.Context(), "quint", ""); err != nil || semver.Compare("v"+v, "v0.34.0") < 0 {
		t.Skipf("quint %s (%v) parses no invariant or observation definitions: 0.34 added them", v, err)
	}
	spec := filepath.Join(t.TempDir(), "named.qnt")
	if err := os.WriteFile(spec, []byte(namedValsSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadModel(t.Context(), "quint", spec, "named")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plain", "nonNegative", "reachedTwo"} {
		if !m.Vals[name] {
			t.Errorf("%s is no val of the model: Vals %v", name, m.Vals)
		}
	}
}
