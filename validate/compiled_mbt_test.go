package validate

import (
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/connect"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// connect's checked-in compiled models (its tests without quint) are a
// compile now, by meaning: gotogether compile -spec -check's comparison.
// An instance module keeps the vals and defs quint's flattening drops.
func TestCompiledMBTModelsAreCurrent(t *testing.T) {
	skip.Quint(t)
	for _, c := range []connect.Config{
		{Spec: "../connect/testdata/bank.qnt"},
		{Spec: "../connect/testdata/instance.qnt", Main: "design"},
	} {
		out := connect.CompiledFor(c.Spec, c.Main)
		cm, err := CompileMBT(t.Context(), c, out)
		if err != nil {
			t.Fatal(err)
		}
		have, _, err := connect.LoadMBT(out)
		if err != nil {
			t.Fatal(err)
		}
		if d := cm.Differs(have); len(d) > 0 {
			t.Errorf("%s is stale (%v): gotogether compile -spec %s%s", out, d, c.Spec, map[bool]string{true: " -main " + c.Main}[c.Main != ""])
		}
		if cm.Spec != filepath.Base(c.Spec) || len(cm.Sources) != 1 {
			t.Errorf("%s: spec %s, sources %v: want the spec beside it", out, cm.Spec, cm.Sources)
		}
		m, err := qeval.Load(cm.IR)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range map[string][]string{"bank.qnt": {"nonNegative", "bobSaves", "balanceOf", "init", "step"}, "instance.qnt": {"atMax", "half", "twice", "init", "step"}}[filepath.Base(c.Spec)] {
			if _, ok := m.Decl(n); !ok {
				t.Errorf("%s keeps no %s", out, n)
			}
		}
	}
}
