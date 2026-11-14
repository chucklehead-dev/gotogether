package qtest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
)

// The model alone, bound by instance module: one binding per design names
// the design, and the broken design (OVERSHOOT) is refuted, the faithful
// one proved, with no constant copied into either binding.
func TestModelClaimsByInstanceModule(t *testing.T) {
	qtest.RequireQuint(t)
	spec, err := filepath.Abs("../binding/testdata/instance/design.qnt")
	if err != nil {
		t.Fatal(err)
	}
	bind := func(module string) string {
		p := filepath.Join(t.TempDir(), module+".binding.yaml")
		if err := os.WriteFile(p, []byte("spec: "+spec+"\nmodule: "+module+"\ninvariants: [withinLimit]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	qtest.Model(t, bind("design")).Expect(t, qtest.Proves("withinLimit"))
	qtest.Model(t, bind("overshoot")).Expect(t, qtest.Refutes("withinLimit"))
}
