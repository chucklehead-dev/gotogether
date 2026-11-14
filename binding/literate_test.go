package binding

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/literate"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// literateCopy is validate/testdata/literate's counter in a directory of
// the test's own, its document edited by edit.
func literateCopy(t *testing.T, edit func([]byte) []byte) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"counter.md", "counter.binding.yaml"} {
		src, err := os.ReadFile(filepath.Join("../validate/testdata/literate", f))
		if err != nil {
			t.Fatal(err)
		}
		if f == "counter.md" {
			src = edit(src)
		}
		if err := os.WriteFile(filepath.Join(dir, f), src, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A binding naming a Markdown document loads its quint blocks as the
// model: Load writes them beside it, SpecPath is that file, DocPath the
// document.
func TestLoadLiterateModel(t *testing.T) {
	dir := literateCopy(t, func(b []byte) []byte { return b })
	b, err := Load(filepath.Join(dir, "counter.binding.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if b.SpecPath() != filepath.Join(dir, "counter.qnt") || b.DocPath() != filepath.Join(dir, "counter.md") {
		t.Fatalf("SpecPath %s, DocPath %s", b.SpecPath(), b.DocPath())
	}
	if err := literate.Check(b.DocPath()); err != nil {
		t.Errorf("the generated model after Load: %v", err)
	}
	// a hand-written model in the generated one's place is never overwritten
	if err := os.WriteFile(b.SpecPath(), []byte("module counter {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(dir, "counter.binding.yaml")); !errors.Is(err, literate.ErrNotGenerated) {
		t.Errorf("over a hand-written counter.qnt: %v", err)
	}
}

// quint's error in a literate model is reported at the document's line,
// in the document's name.
func TestLiterateTypecheckErrorAtTheDocumentLine(t *testing.T) {
	skip.Quint(t)
	dir := literateCopy(t, func(b []byte) []byte {
		return bytes.Replace(b, []byte("val belowMax = n < MAX"), []byte("val belowMax = n < MAXX"), 1)
	})
	b, err := Load(filepath.Join(dir, "counter.binding.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = LoadModel(t.Context(), "", b.SpecPath(), b.Module)
	if err == nil || !strings.Contains(err.Error(), "counter.md:23:") || !strings.Contains(err.Error(), "MAXX") || strings.Contains(err.Error(), "counter.qnt:") {
		t.Fatalf("want quint's error at counter.md:23, not in counter.qnt:\n%v", err)
	}
}
