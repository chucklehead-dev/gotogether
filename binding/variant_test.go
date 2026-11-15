package binding

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A variant is the binding under its constants and classifications: the
// section's constants with the variant's over them, its holds and allows in
// place of the section's where it gives them; names are required and
// distinct.
func TestVariants(t *testing.T) {
	b, err := Load("../validate/testdata/design.yaml")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := b.Variant("lease")
	if err != nil {
		t.Fatal(err)
	}
	if lease.Simulate.Constants["DESIGN"] != `"lease"` || len(lease.Simulate.Holds) != 0 || !slices.Equal(lease.Simulate.Allows, []string{"noStaleWrite", "oldNeverWrites"}) || lease.Simulate.Variants != nil {
		t.Errorf("lease: %+v", lease.Simulate)
	}
	fence, err := b.Variant("fence")
	if err != nil {
		t.Fatal(err)
	}
	// fence gives no lists: the section's stand
	if !slices.Equal(fence.Simulate.Holds, []string{"noStaleWrite"}) || !slices.Equal(fence.Simulate.Allows, []string{"oldNeverWrites"}) {
		t.Errorf("fence: %+v", fence.Simulate)
	}
	if b.Constants["DESIGN"] != `"fence"` || len(b.Simulate.Constants) != 0 || len(b.Simulate.Variants) != 2 {
		t.Errorf("the binding itself changed: %+v", b.Simulate)
	}
	if _, err := b.Variant("object"); err == nil || !strings.Contains(err.Error(), `has no "object": its variants are lease, fence`) {
		t.Errorf("an unknown variant: %v", err)
	}

	src, err := os.ReadFile("../validate/testdata/design.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, c := range []struct{ from, to, want string }{
		{"    - name: fence\n", "    - name: lease\n", `simulate.variants names "lease" twice`},
		{"    - name: lease\n", "    - name: \"\"\n", "simulate.variants[0] has no name"},
	} {
		p := filepath.Join(dir, "b.yaml")
		if err := os.WriteFile(p, []byte(strings.Replace(string(src), c.from, c.to, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v, want %q", c.to, err, c.want)
		}
	}
}
