package literate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const doc = "# Counter\n" + // 1
	"\n" + // 2
	"The counter only grows.\n" + // 3
	"\n" + // 4
	"```quint\n" + // 5
	"module counter {\n" + // 6
	"  var n: int\n" + // 7
	"```\n" + // 8
	"\n" + // 9
	"A Go block is not the model:\n" + // 10
	"\n" + // 11
	"```go\n" + // 12
	"var n int\n" + // 13
	"```\n" + // 14
	"\n" + // 15
	"Its actions:\n" + // 16
	"\n" + // 17
	"````quint\n" + // 18
	"  action init = n' = 0\n" + // 19
	"  action step = n' = n + 1\n" + // 20
	"  val small = n < 3\n" + // 21
	"}\n" + // 22
	"````\n" // 23

func TestTangleKeepsQuintBlocksAndTheirLines(t *testing.T) {
	tg, err := Tangle("counter.md", []byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(tg.Source), "\n"), "\n")
	body := lines[2 : len(lines)-1]
	want := []string{"module counter {", "  var n: int", "  action init = n' = 0", "  action step = n' = n + 1", "  val small = n < 3", "}"}
	if strings.Join(body, "\n") != strings.Join(want, "\n") || tg.Blocks != 2 {
		t.Fatalf("tangled %d blocks:\n%s", tg.Blocks, tg.Source)
	}
	if !strings.HasPrefix(lines[0], Header+"counter.md") || !strings.HasPrefix(lines[1], digestPrefix+tg.Digest) {
		t.Errorf("header %q %q", lines[0], lines[1])
	}
	m := lineMap(tg.Source)
	for gen, docLine := range map[int]int{3: 6, 4: 7, 5: 19, 8: 22} {
		if m[gen] != docLine {
			t.Errorf("generated line %d maps to %d, want document line %d (map %v)", gen, m[gen], docLine, m)
		}
	}
	if _, ok := m[1]; ok {
		t.Error("the header maps to a document line")
	}
	if d := tg.Definitions(); d["small"] != 21 || d["init"] != 19 {
		t.Errorf("definitions %v, want small at 21 and init at 19", d)
	}
}

func TestTangleRefusals(t *testing.T) {
	for _, c := range []struct{ doc, want string }{
		{"# No model\n\n```go\nx\n```\n", "no ```quint block"},
		{"```quint\nmodule m {}\n", "doc.md:1: a fenced block is never closed: close it with ```"},
	} {
		if _, err := Tangle("doc.md", []byte(c.doc)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Tangle(%q): %v, want %q", c.doc, err, c.want)
		}
	}
}

// A position in the generated file is the document's (relative as
// written); one in another file, or on the generated header, stays as it
// is.
func TestRewriteMapsPositionsToTheDocument(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "counter.md")
	if err := os.WriteFile(md, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, gen, err := Write(md); err != nil || gen != filepath.Join(dir, "counter.qnt") {
		t.Fatalf("Write: %s, %v", gen, err)
	}
	in := "counter.qnt:7:9 - error: [QNT404] Name 'm' not found; at counter.qnt:1:1; other.qnt:7:1"
	want := "counter.md:21:9 - error: [QNT404] Name 'm' not found; at counter.qnt:1:1; other.qnt:7:1"
	if got := Rewrite(in, dir); got != want {
		t.Errorf("Rewrite:\n got %s\nwant %s", got, want)
	}
}

func TestWriteAndCheck(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "counter.md")
	gen := filepath.Join(dir, "counter.qnt")
	if err := os.WriteFile(md, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Check(md); err == nil || !strings.Contains(err.Error(), "missing: run gotogether tangle") {
		t.Errorf("before Write: %v", err)
	}
	if _, _, err := Write(md); err != nil {
		t.Fatal(err)
	}
	if err := Check(md); err != nil {
		t.Errorf("after Write: %v", err)
	}
	// the document changes: the committed model is stale, named so
	if err := os.WriteFile(md, []byte(strings.Replace(doc, "n < 3", "n < 4", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Check(md); err == nil || !strings.Contains(err.Error(), "stale: generated from another version of") {
		t.Errorf("a stale model: %v", err)
	}
	if _, _, err := Write(md); err != nil || Check(md) != nil {
		t.Errorf("rewritten: %v, %v", err, Check(md))
	}
	// a hand-written model of that name is never overwritten
	if err := os.WriteFile(gen, []byte("module counter {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Write(md); !errors.Is(err, ErrNotGenerated) {
		t.Errorf("over a hand-written file: %v", err)
	}
	if b, _ := os.ReadFile(gen); string(b) != "module counter {}\n" {
		t.Errorf("the hand-written file changed: %q", b)
	}
}

// The matrix replaces what is between its markers, nothing outside them,
// and a document without them, or with them twice, is refused.
func TestWriteMarked(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "design.md")
	write := func(s string) {
		if err := os.WriteFile(doc, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("# Design\n\nbefore\n" + MatrixBegin + "\nold table\n" + MatrixEnd + "\nafter\n")
	if changed, err := WriteMarked(doc, MatrixBegin, MatrixEnd, "| new |\n"); err != nil || !changed {
		t.Fatalf("first write: %v, %v", changed, err)
	}
	got, _ := os.ReadFile(doc)
	if want := "# Design\n\nbefore\n" + MatrixBegin + "\n| new |\n" + MatrixEnd + "\nafter\n"; string(got) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if changed, err := WriteMarked(doc, MatrixBegin, MatrixEnd, "| new |\n"); err != nil || changed {
		t.Fatalf("the same table again: %v, %v", changed, err)
	}
	for body, want := range map[string]string{
		"no markers\n": "has no " + MatrixBegin,
		MatrixBegin + "\na\n" + MatrixEnd + "\n" + MatrixBegin + "\nb\n" + MatrixEnd + "\n": "more than once",
		MatrixEnd + "\n" + MatrixBegin + "\n":                                               "before",
	} {
		write(body)
		if _, err := WriteMarked(doc, MatrixBegin, MatrixEnd, "x"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", body, err, want)
		}
	}
}

// A matrix written into a document whose model is current leaves that
// model current: the write changes the document's digest and line map,
// so the model is written again (simulate -write-matrix, then tangle
// -check). A model already stale, or none, is left as it was.
func TestWriteMarkedKeepsTheModelCurrent(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "design.md")
	body := "# Design\n\n```quint\nmodule design {\n  var x: int\n}\n```\n\n" + MatrixBegin + "\nold table\n" + MatrixEnd + "\n"
	if err := os.WriteFile(doc, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Write(doc); err != nil {
		t.Fatal(err)
	}
	if changed, err := WriteMarked(doc, MatrixBegin, MatrixEnd, "| new |\n"); err != nil || !changed {
		t.Fatalf("write: %v, %v", changed, err)
	}
	if err := Check(doc); err != nil {
		t.Errorf("the model was current before the matrix was written, not after: %v", err)
	}

	// stale before (the prose edited after tangling): left stale
	if err := os.WriteFile(doc, []byte("edited\n\n"+body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteMarked(doc, MatrixBegin, MatrixEnd, "| newer |\n"); err != nil {
		t.Fatal(err)
	}
	if err := Check(doc); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Errorf("a stale model rewritten by a matrix write: %v", err)
	}

	// none before: none made
	if err := os.Remove(GeneratedPath(doc)); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteMarked(doc, MatrixBegin, MatrixEnd, "| newest |\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(GeneratedPath(doc)); !os.IsNotExist(err) {
		t.Errorf("a model made where none was: %v", err)
	}
}
