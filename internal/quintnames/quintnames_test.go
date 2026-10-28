package quintnames

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// quintPackage is the directory of the quint on PATH's compiled sources
// (dist/src, where its cli.js is), or "" for a quint that is no npm
// package.
func quintPackage(t *testing.T) string {
	bin, err := exec.LookPath("quint")
	if err != nil {
		skip.Optional(t, "quint not on PATH: the table is checked against quint itself")
		return ""
	}
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(real)
}

// The table's built-ins are quint's own list: a name quint adds is
// caught here, the day its version is bumped, not in a newcomer's first
// model.
func TestBuiltinsAreQuints(t *testing.T) {
	dir := quintPackage(t)
	src, err := os.ReadFile(filepath.Join(dir, "names", "base.js"))
	if err != nil {
		skip.Optional(t, fmt.Sprintf("quint's names/base.js not found next to its cli.js (%v): a quint not installed by npm", err))
		return
	}
	list := regexp.MustCompile(`(?s)exports\.builtinNames = \[(.*?)\];`).FindSubmatch(src)
	if list == nil {
		t.Fatalf("quint's names/base.js has no builtinNames list: its layout changed; read its built-ins another way")
	}
	var quints []string
	for _, m := range regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(list[1], -1) {
		if n := string(m[1]); !strings.Contains(n, "::") {
			quints = append(quints, n)
		}
	}
	slices.Sort(quints)
	ours := slices.Sorted(slices.Values(Builtins))
	if !slices.Equal(quints, ours) {
		var missing, extra []string
		for _, n := range quints {
			if !slices.Contains(ours, n) {
				missing = append(missing, n)
			}
		}
		for _, n := range ours {
			if !slices.Contains(quints, n) {
				extra = append(extra, n)
			}
		}
		t.Errorf("Builtins differ from quint's: missing %v, not quint's %v (update the table, and AGENTS.md's list)", missing, extra)
	}
}

// The table's keywords are quint's lexer's: its literal tokens that are
// identifiers, true and false (its BOOL token's), but from and as, which
// only an import's grammar reads.
func TestKeywordsAreQuints(t *testing.T) {
	dir := quintPackage(t)
	src, err := os.ReadFile(filepath.Join(dir, "generated", "QuintLexer.js"))
	if err != nil {
		skip.Optional(t, fmt.Sprintf("quint's generated/QuintLexer.js not found next to its cli.js (%v): a quint not installed by npm", err))
		return
	}
	list := regexp.MustCompile(`(?s)_LITERAL_NAMES = \[(.*?)\];`).FindSubmatch(src)
	if list == nil {
		t.Fatalf("quint's QuintLexer.js has no _LITERAL_NAMES: its layout changed; read its keywords another way")
	}
	quints := []string{"true", "false"}
	for _, m := range regexp.MustCompile(`"'([A-Za-z][A-Za-z0-9_]*)'"`).FindAllSubmatch(list[1], -1) {
		if n := string(m[1]); n != "from" && n != "as" && !slices.Contains(Builtins, n) {
			quints = append(quints, n)
		}
	}
	slices.Sort(quints)
	if ours := slices.Sorted(slices.Values(Keywords)); !slices.Equal(quints, ours) {
		t.Errorf("Keywords differ from quint's lexer's:\n  quint's: %v\n  ours:    %v\n(update the table, and AGENTS.md's list)", quints, ours)
	}
}

// Every name of the table is refused as a parameter, and names next to
// them (init, step, from, as, a renamed one) are taken, by quint parse:
// the built-ins in one module (each its own QNT101), and a keyword of
// each kind in its own (a parse error stops at the first): the others are
// the lexer's tokens of the same kinds (TestKeywordsAreQuints), and a
// module each cost a second of quint per name.
func TestReservedRefusedByQuint(t *testing.T) {
	quintPackage(t)
	if testing.Short() {
		t.Skip("parses a module per keyword")
	}
	dir := t.TempDir()
	parse := func(name string, params []string) (string, error) {
		var b strings.Builder
		b.WriteString("module m {\n  var v: int\n")
		for i, p := range params {
			fmt.Fprintf(&b, "  action a%d(%s: int): bool = v' = 1\n", i, p)
		}
		b.WriteString("}\n")
		sub := filepath.Join(dir, name)
		if err := os.Mkdir(sub, 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(sub, "m.qnt"), []byte(b.String()), 0o644); err != nil {
			return "", err
		}
		cmd := exec.Command("quint", "parse", "m.qnt")
		cmd.Dir = sub
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := parse("builtins", Builtins)
	if err == nil {
		t.Fatalf("quint parsed a parameter of every built-in's name")
	}
	for _, n := range Builtins {
		if !strings.Contains(out, "Built-in name '"+n+"' is redefined") {
			t.Errorf("quint takes a parameter named %s, which the table says is a built-in:\n%s", n, out)
		}
	}
	if out, err := parse("taken", []string{"init", "step", "from", "as", "item_", "x"}); err != nil {
		t.Errorf("quint refuses a parameter the table takes: %v\n%s", err, out)
	}
	var wg sync.WaitGroup
	for _, n := range []string{"match", "int", "temporal", "true", "all"} {
		if !slices.Contains(Keywords, n) {
			t.Errorf("%s is no keyword of the table", n)
		}
		wg.Go(func() {
			if out, err := parse("kw-"+n, []string{n}); err == nil {
				t.Errorf("quint takes a parameter named %s, which the table says is a keyword:\n%s", n, out)
			}
		})
	}
	wg.Wait()
}

func TestRename(t *testing.T) {
	for _, c := range []struct{ in, out string }{{"item", "item_"}, {"to", "to_"}, {"match", "match_"}, {"sku", "sku"}, {"init", "init"}} {
		if got := Rename(c.in); got != c.out {
			t.Errorf("Rename(%s) = %s, want %s", c.in, got, c.out)
		}
		if o, ok := Original(Rename(c.in)); ok != (c.in != c.out) || ok && o != c.in {
			t.Errorf("Original(%s) = %s, %v", Rename(c.in), o, ok)
		}
	}
}
