package literate

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Named markers are pairs of their own: two models' tables in one
// document, each written alone, the other's untouched; the unnamed pair
// is a third; MarkerNames lists them.
func TestNamedMarkers(t *testing.T) {
	ab, ae := Markers("invariants", "attempts")
	pb, pe := Markers("invariants", "publication")
	ub, ue := Markers("invariants", "")
	if ab != "<!-- gotogether:invariants attempts -->" || ae != "<!-- /gotogether:invariants attempts -->" || ub != "<!-- gotogether:invariants -->" {
		t.Fatalf("markers %q %q %q", ab, ae, ub)
	}
	doc := filepath.Join(t.TempDir(), "design.md")
	body := "# Two\n\n" + ab + "\nA\n" + ae + "\n\n" + pb + "\nP\n" + pe + "\n\n" + ub + "\nU\n" + ue + "\n"
	if err := os.WriteFile(doc, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteMarked(doc, ab, ae, "| attempts |\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteMarked(doc, pb, pe, "| publication |\n"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(doc)
	want := strings.NewReplacer("\nA\n", "\n| attempts |\n", "\nP\n", "\n| publication |\n").Replace(body)
	if string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if names := MarkerNames(string(got), "invariants"); !slices.Equal(names, []string{"attempts", "publication", ""}) {
		t.Errorf("names %q", names)
	}
	if names := MarkerNames("<!-- gotogether:invariantsX -->\n<!-- gotogether:actions a -->\n", "invariants"); len(names) != 0 {
		t.Errorf("another kind's markers: %q", names)
	}
}

// WriteMarkedText writes a document as plain Markdown: a model tangled
// from it before (by gotogether, current) is not written again, where
// WriteMarked would keep it current.
func TestWriteMarkedText(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "design.md")
	body := "# Design\n\n```quint\nmodule design {\n  var x: int\n}\n```\n\n" + MatrixBegin + "\nold\n" + MatrixEnd + "\n"
	if err := os.WriteFile(doc, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Write(doc); err != nil {
		t.Fatal(err)
	}
	if changed, err := WriteMarkedText(doc, MatrixBegin, MatrixEnd, "| new |\n"); err != nil || !changed {
		t.Fatalf("write: %v, %v", changed, err)
	}
	if err := Check(doc); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Errorf("the model was written again from a plain document: %v", err)
	}
}
