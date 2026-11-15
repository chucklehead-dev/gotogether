package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

// tangle writes a document's model; -check passes while it is current,
// and fails (exit 1, naming the fix) once the document changes.
func TestTangleAndCheck(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile("../../validate/testdata/literate/counter.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "counter.md")
	if err := os.WriteFile(doc, src, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code, err := tangle(&out, []string{"-check", doc}); code != 1 || err != nil || !strings.Contains(out.String(), "missing: run gotogether tangle") {
		t.Fatalf("before tangling: %d, %v\n%s", code, err, out.String())
	}
	out.Reset()
	if code, err := tangle(&out, []string{doc}); code != 0 || err != nil || !strings.Contains(out.String(), "2 quint blocks") {
		t.Fatalf("tangle: %d, %v\n%s", code, err, out.String())
	}
	out.Reset()
	if code, err := tangle(&out, []string{"-check", doc}); code != 0 || err != nil {
		t.Fatalf("current: %d, %v\n%s", code, err, out.String())
	}
	if err := os.WriteFile(doc, bytes.Replace(src, []byte("MAX = 3"), []byte("MAX = 4"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code, err := tangle(&out, []string{"-check", doc}); code != 1 || err != nil || !strings.Contains(out.String(), "stale: generated from another version of") {
		t.Fatalf("stale: %d, %v\n%s", code, err, out.String())
	}
	if code, err := tangle(&out, []string{"counter.qnt"}); code != 2 || err == nil {
		t.Fatalf("a .qnt is no document: %d, %v", code, err)
	}
}

// A literate model's failing invariant is reported at its document line,
// in the text and as the check's source in the JSON report.
func TestSimulateLiterateModelNamesTheDocumentLine(t *testing.T) {
	skip.Quint(t)
	b := "../../validate/testdata/literate/counter.binding.yaml"
	_, text := capture(t, func() (int, error) { return simulate(t.Context(), []string{"-graph", "-binding", b}) })
	if !strings.Contains(text, "FAIL    belowMax (../../validate/testdata/literate/counter.md:23):") {
		t.Errorf("the failing invariant does not name its document line:\n%s", text)
	}
	_, out := capture(t, func() (int, error) { return simulate(t.Context(), []string{"-json", "-graph", "-binding", b}) })
	var d validate.Document
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for _, c := range d.Checks {
		sources[c.Name] = c.Source
	}
	if !strings.HasSuffix(sources["belowMax"], "counter.md:23") || !strings.HasSuffix(sources["nonNegative"], "counter.md:21") {
		t.Errorf("sources %v, want belowMax at counter.md:23 and nonNegative at 21", sources)
	}
}
