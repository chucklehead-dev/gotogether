package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// fixture is the invariants fixture's directory: a model, a counter
// annotated on a method's doc, a statement and two tests, a binding, and
// a design document whose table is checked in (the golden).
const fixture = "testdata/invariants"

// copyFixture copies the fixture into a new directory.
func copyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	err := filepath.WalkDir(fixture, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(fixture, path)
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		to := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		return os.WriteFile(to, src, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// edit replaces old with new in the file at path, once.
func edit(t *testing.T, path, old, new string) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(src, []byte(old)) {
		t.Fatalf("%s has no %q", path, old)
	}
	if err := os.WriteFile(path, bytes.Replace(src, []byte(old), []byte(new), 1), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The checked-in document's table is what the fixture generates now,
// from this directory as from the fixture's (paths are the document's):
// -check passes, warning of the property nothing enforces or checks, and
// -require makes that warning fail.
func TestInvariantsGolden(t *testing.T) {
	skip.Quint(t)
	doc := fixture + "/docs/walkthrough.md"
	var out, errw bytes.Buffer
	args := []string{"-spec", fixture + "/model/counter.qnt", "-check", doc, "-require", "enforced", fixture + "/counter"}
	code, err := invariants(t.Context(), &out, &errw, args)
	if err != nil || !strings.Contains(out.String(), "ok   "+doc+": current (3 properties of model counter: 2 enforced, 2 checked)") {
		t.Fatalf("the checked-in table: %d, %v\n%s%s(write it again: cd %s && gotogether invariants -spec model/counter.qnt -write docs/walkthrough.md ./counter)", code, err, out.String(), errw.String(), fixture)
	}
	want := "warning: property atCap: nothing enforces it: put //quint:enforces atCap on the code that does\n" +
		"warning: property atCap: no test checks it: put //quint:checks atCap on the test that does\n" +
		"FAIL property atCap: nothing enforces it: put //quint:enforces atCap on the code that does\n"
	if code != 1 || errw.String() != want {
		t.Errorf("-require enforced: exit %d\n%s\nwant exit 1 and\n%s", code, errw.String(), want)
	}
}

// -write writes the table and -check finds it current; a line moved in
// the code makes it stale, naming the row and the command that writes
// it, and -check writes nothing; a directive naming no property fails
// under -check as under -write, at its file:line, with the properties.
func TestInvariantsWriteCheck(t *testing.T) {
	skip.Quint(t)
	dir := copyFixture(t)
	doc := filepath.Join(dir, "docs", "walkthrough.md")
	edit(t, doc, "| `atCap` | | — | — |\n", "")
	spec, code := filepath.Join(dir, "model", "counter.qnt"), filepath.Join(dir, "counter")
	run := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errw bytes.Buffer
		n, err := invariants(t.Context(), &out, &errw, args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return n, out.String(), errw.String()
	}
	if n, out, _ := run("-spec", spec, "-check", doc, code); n != 1 || !strings.Contains(out, "FAIL "+doc+": stale: row atCap added: run gotogether invariants -spec "+spec+" -write "+doc+" "+code+"\n") {
		t.Fatalf("a row missing: %d\n%s", n, out)
	}
	if n, out, _ := run("-spec", spec, "-write", doc, code); n != 0 || out != doc+": written (3 properties of model counter: 2 enforced, 2 checked)\n" {
		t.Fatalf("write: %d\n%s", n, out)
	}
	if n, out, _ := run("-spec", spec, "-write", doc, code); n != 0 || !strings.Contains(out, "current, unchanged") {
		t.Fatalf("write again: %d\n%s", n, out)
	}
	written, _ := os.ReadFile(doc)
	golden, _ := os.ReadFile(fixture + "/docs/walkthrough.md")
	if !bytes.Equal(written, golden) {
		t.Errorf("written from another directory, the document differs from the golden:\n%s", written)
	}
	// Reset moves down a line: its row is stale, the others are not
	edit(t, filepath.Join(code, "counter.go"), "// Reset starts again from zero.\n", "// Reset starts again from zero,\n// whatever the count.\n")
	if n, out, _ := run("-spec", spec, "-check", doc, code); n != 1 || !strings.Contains(out, "stale: row nonNegative changed: run gotogether invariants") {
		t.Errorf("a moved line: %d\n%s", n, out)
	}
	if now, _ := os.ReadFile(doc); !bytes.Equal(now, written) {
		t.Errorf("-check wrote the document")
	}
	// a misspelled property, under -check: an error at its line
	edit(t, filepath.Join(code, "counter.go"), "//quint:enforces withinCap\n", "//quint:enforces withinCapp\n")
	n, out, errw := run("-spec", spec, "-check", doc, code)
	at := filepath.Join(code, "counter.go") + ":17: //quint:enforces names withinCapp: model counter has no property withinCapp: did you mean withinCap? name one of model counter's properties: withinCap, nonNegative, atCap\n"
	if n != 1 || errw != at || out != "FAIL 1 directive naming no property of model counter, or annotating nothing: the table is not written (fix each as it says)\n" {
		t.Errorf("unknown property: %d\n%s%s\nwant\n%s", n, out, errw, at)
	}
}

// -binding takes the model from a binding, and its invariants as the
// properties: a val of the model it does not check is named as such.
// Qualified names resolve through the instance (counter::withinCap) and
// the module that declares the val (counterBase.nonNegative).
func TestInvariantsBinding(t *testing.T) {
	skip.Quint(t)
	dir := copyFixture(t)
	b := filepath.Join(dir, "counter.binding.yaml")
	var out, errw bytes.Buffer
	if n, err := invariants(t.Context(), &out, &errw, []string{"-binding", b, "-require", "enforced,checked", filepath.Join(dir, "counter")}); n != 0 || err != nil || errw.Len() != 0 || strings.Contains(out.String(), "atCap") || strings.Count(out.String(), "\n") != 4 || !strings.Contains(out.String(), "| `nonNegative` | n is never negative. | `Counter.Reset`") {
		t.Fatalf("%d, %v\n%s%s", n, err, out.String(), errw.String())
	}
	edit(t, filepath.Join(dir, "counter", "counter_test.go"), "//quint:checks withinCap\n", "//quint:checks withinCap atCap\n")
	out.Reset()
	if n, err := invariants(t.Context(), &out, &errw, []string{"-binding", b, filepath.Join(dir, "counter")}); n != 1 || err != nil || !strings.Contains(errw.String(), "counter_test.go:7: //quint:checks names atCap: atCap is a val of model counterBase that the binding does not check: add it to the binding's invariants (or eventually), or name one it checks") {
		t.Errorf("a val the binding does not check: %d, %v\n%s%s", n, err, out.String(), errw.String())
	}
}

// A bad invocation exits 2 and says what to give, before quint runs.
func TestInvariantsFlags(t *testing.T) {
	doc := fixture + "/docs/walkthrough.md"
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "invariants needs -spec model.qnt -main M, or -binding b.yaml: gotogether invariants -spec"},
		{[]string{"-binding", "b.yaml", "-main", "m"}, "-binding names the spec and its module"},
		{[]string{"-spec", "m.qnt", "-write", doc, "-check", doc}, "-write writes the table and -check compares it, writing nothing: give one"},
		{[]string{"-spec", "m.qnt", "-require", "tested"}, "-require tested: give enforced, checked, or enforced,checked"},
		{[]string{"-spec", "m.qnt", "-check", "none.md"}, "the document none.md:"},
		{[]string{"-spec", "m.qnt", "nowhere/..."}, "nowhere/... names no directory"},
		{[]string{"-nope"}, "invariants: flag provided but not defined: -nope (gotogether invariants -h lists its flags)"},
		{[]string{"-spec", "nowhere.qnt", fixture + "/counter"}, "the spec nowhere.qnt does not exist"},
	} {
		var out, errw bytes.Buffer
		code, err := invariants(t.Context(), &out, &errw, c.args)
		if code != 2 || err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("invariants %v: %d, %v; want exit 2 and %q", c.args, code, err, c.want)
		}
	}
	var out bytes.Buffer
	if code, err := invariants(t.Context(), &out, &out, []string{"-h"}); code != 0 || err != nil || !strings.Contains(out.String(), commands["invariants"].purpose) || !strings.Contains(out.String(), "-require") {
		t.Errorf("-h: %d, %v\n%s", code, err, out.String())
	}
}
