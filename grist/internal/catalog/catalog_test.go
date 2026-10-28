package catalog

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// Every name quint keeps (its built-ins, its lexer's keywords) is one a
// generated parameter may not take, read from quint itself: a hand-kept
// list once missed item, and missed int, str and bool here. A quint not
// installed by npm skips.
func TestQuintTakenCoversQuint(t *testing.T) {
	bin, err := exec.LookPath("quint")
	if err != nil {
		t.Skip("quint not on PATH")
	}
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(real) // dist/src, beside cli.js
	for _, c := range []struct{ file, list, item string }{
		{"names/base.js", `(?s)exports\.builtinNames = \[(.*?)\];`, `'([A-Za-z][A-Za-z0-9_]*)'`},
		{"generated/QuintLexer.js", `(?s)_LITERAL_NAMES = \[(.*?)\];`, `"'([A-Za-z][A-Za-z0-9_]*)'"`},
	} {
		src, err := os.ReadFile(filepath.Join(dir, c.file))
		if err != nil {
			t.Skipf("quint's %s not found beside its cli.js: a quint not installed by npm", c.file)
		}
		list := regexp.MustCompile(c.list).FindSubmatch(src)
		if list == nil {
			t.Fatalf("quint's %s has no list matching %s: its layout changed", c.file, c.list)
		}
		for _, m := range regexp.MustCompile(c.item).FindAllSubmatch(list[1], -1) {
			if n := string(m[1]); !QuintTaken[n] {
				t.Errorf("%s is quint's (%s), and QuintTaken lacks it: a parameter so named would not parse", n, c.file)
			}
		}
	}
}
