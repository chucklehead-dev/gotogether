package validate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/connect"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// connect's checked-in compiled models (its tests without quint) are a
// compile now, by meaning: gotogether compile -spec -check's comparison.
// An instance module keeps the vals and defs quint's flattening drops.
// They are kept gzipped (connect.CompiledExt), and two compiles write the
// same gzipped bytes.
func TestCompiledMBTModelsAreCurrent(t *testing.T) {
	skip.Quint(t)
	for _, c := range []MBTOptions{
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
		if !strings.HasSuffix(out, connect.CompiledExt) {
			t.Errorf("connect.CompiledFor names %s: want the gzipped convention %s", out, connect.CompiledExt)
		}
		again, err := CompileMBT(t.Context(), c, out)
		if err != nil {
			t.Fatal(err)
		}
		first, second := filepath.Join(t.TempDir(), "a"+connect.CompiledExt), filepath.Join(t.TempDir(), "b"+connect.CompiledExt)
		if cm.Write(first) != nil || again.Write(second) != nil {
			t.Fatal("writing the compiled models")
		}
		if a, b := mustReadFile(t, first), mustReadFile(t, second); !bytes.Equal(a, b) || !bytes.HasPrefix(a, gzipMagic) {
			t.Errorf("%s: two compiles wrote %d and %d bytes (gzip %v): want the same gzipped bytes", c.Spec, len(a), len(b), bytes.HasPrefix(a, gzipMagic))
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

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
