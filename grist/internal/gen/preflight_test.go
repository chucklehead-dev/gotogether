package gen_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/gen"
)

type note struct {
	Item string `json:"item"`
}

func notes() *grist.App {
	a := grist.New("notes")
	noted := grist.NewEvent[note](a, "noted")
	grist.NewCommand(a, "note", func(r *grist.Reads, c note) ([]grist.Event, error) { return []grist.Event{noted.Of(c)}, nil }, grist.Unconditioned("a note reads nothing"), grist.Appends(noted), grist.Authorize(grist.Authenticated))
	return a
}

func put(t *testing.T, dir, path string, b []byte) {
	t.Helper()
	p := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
func get(t *testing.T, dir, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func baseline(t *testing.T) (*grist.App, string) {
	t.Helper()
	a := notes()
	dir := t.TempDir()
	files, err := gen.Starter(a)
	if err != nil {
		t.Fatal(err)
	}
	for path, b := range files {
		put(t, dir, path, b)
	}
	p := gen.Inspect(a, dir)
	if !p.CanGenerate() {
		t.Fatal(p.String())
	}
	next, err := p.Apply()
	if err != nil || !next.OK() {
		t.Fatalf("baseline: %v\n%s", err, next.String())
	}
	return a, dir
}
func has(p *gen.Preflight, code, path, symbol string) bool {
	for _, f := range p.Findings {
		if f.Code == code && (path == "" || f.Path == path) && (symbol == "" || f.Symbol == symbol) {
			return true
		}
	}
	return false
}

func TestPreflightAggregatesMechanicalAndHandwrittenWorkWithoutWriting(t *testing.T) {
	a, dir := baseline(t)
	put(t, dir, "notes.binding.yaml", []byte("# stale binding\n"))
	rules := bytes.Replace(get(t, dir, "model/notes_rules.qnt"), []byte("noteNeeds"), []byte("noteNeedsRemoved"), 1)
	put(t, dir, "model/notes_rules.qnt", rules)
	own := bytes.ReplaceAll(get(t, dir, "model/notes.qnt"), []byte("import notesEvents.*"), []byte("// removed import notesEvents.*"))
	put(t, dir, "model/notes.qnt", own)
	if err := os.Remove(filepath.Join(dir, "grist.lock")); err != nil {
		t.Fatal(err)
	}
	p := gen.Inspect(a, dir)
	for _, want := range []struct{ code, path, symbol string }{{"model.generated_stale", "notes.binding.yaml", ""}, {"model.generated_stale", "grist.lock", ""}, {"model.definition_missing", "model/notes_rules.qnt", "noteNeeds"}, {"model.import_missing", "model/notes.qnt", "notes/notesEvents"}} {
		if !has(p, want.code, want.path, want.symbol) {
			t.Fatalf("missing %+v:\n%s", want, p.String())
		}
	}
	if p.OK() || !p.CanGenerate() {
		t.Fatalf("manual errors should fail check but permit generated changes:\n%s", p.String())
	}
	if string(get(t, dir, "notes.binding.yaml")) != "# stale binding\n" || !bytes.Equal(get(t, dir, "model/notes_rules.qnt"), rules) {
		t.Fatal("inspection wrote files")
	}
	for _, c := range p.Checks {
		if (c.Name == "definitions" || c.Name == "imports" || c.Name == "generated_files") && c.Status != "fail" {
			t.Fatalf("error finding acquired passing check: %+v", c)
		}
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var result struct{ OK, CanGenerate bool }
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	if result.OK || !result.CanGenerate || !bytes.HasPrefix(encoded, []byte(`{"format":`)) {
		t.Fatalf("derived JSON status: %s", encoded)
	}
	next, err := p.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if next.OK() || has(next, "model.generated_stale", "", "") || !has(next, "model.definition_missing", "", "noteNeeds") {
		t.Fatalf("generation hid manual work or failed to converge:\n%s", next.String())
	}
	if !bytes.Equal(get(t, dir, "model/notes_rules.qnt"), rules) || !bytes.Equal(get(t, dir, "model/notes.qnt"), own) {
		t.Fatal("generation changed handwritten files")
	}
}

func TestPreflightRefusesStalePlanBeforeAnyWrite(t *testing.T) {
	a, dir := baseline(t)
	put(t, dir, "notes.binding.yaml", []byte("# stale\n"))
	p := gen.Inspect(a, dir)
	rules := append(get(t, dir, "model/notes_rules.qnt"), []byte("\n// edited after preflight\n")...)
	put(t, dir, "model/notes_rules.qnt", rules)
	if _, err := p.Apply(); err == nil || !strings.Contains(err.Error(), "plan is stale") {
		t.Fatalf("stale plan accepted: %v", err)
	}
	if string(get(t, dir, "notes.binding.yaml")) != "# stale\n" {
		t.Fatal("stale plan partially wrote its files")
	}
}

func TestPreflightRemovesOnlyOwnedObsoleteVariants(t *testing.T) {
	a, dir := baseline(t)
	binding := get(t, dir, "notes.calls.binding.yaml")
	put(t, dir, "notes.retired.binding.yaml", binding)
	put(t, dir, "notes.handwritten.binding.yaml", []byte("# handwritten binding\n"))
	foreign := bytes.Replace(binding, []byte("from the notes App's catalog"), []byte("from the other App's catalog"), 1)
	put(t, dir, "notes.foreign.binding.yaml", foreign)
	p := gen.Inspect(a, dir)
	if !has(p, "model.generated_obsolete", "notes.retired.binding.yaml", "") {
		t.Fatal(p.String())
	}
	next, err := p.Apply()
	if err != nil || !next.OK() {
		t.Fatalf("apply: %v\n%s", err, next.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.retired.binding.yaml")); !os.IsNotExist(err) {
		t.Fatal("obsolete generated binding remains")
	}
	if string(get(t, dir, "notes.handwritten.binding.yaml")) != "# handwritten binding\n" {
		t.Fatal("removed handwritten variant")
	}
	if !bytes.Equal(get(t, dir, "notes.foreign.binding.yaml"), foreign) {
		t.Fatal("removed another app's generated file")
	}
	if len(next.Changes) != 0 {
		t.Fatalf("post-apply plan not at fixpoint: %+v", next.Changes)
	}
}

func TestPreflightRulesAnnotationErrorCannotAcquirePassingCheck(t *testing.T) {
	a, dir := baseline(t)
	rules := append(get(t, dir, "model/notes_rules.qnt"), []byte("\n// grist: eventually\nval unsupported = true\n")...)
	put(t, dir, "model/notes_rules.qnt", rules)
	p := gen.Inspect(a, dir)
	if p.CanGenerate() || !has(p, "model.marks_invalid", "model/notes_rules.qnt", "") {
		t.Fatal(p.String())
	}
	for _, c := range p.Checks {
		if c.Name == "annotations" && c.Status != "fail" {
			t.Fatalf("invalid rules annotation check: %+v", c)
		}
	}
	first, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(gen.Inspect(a, dir))
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("report order is nondeterministic: %v", err)
	}
}

func TestPreflightRejectsOutputSymlinksWithoutWritingTheirTargets(t *testing.T) {
	a, dir := baseline(t)
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "notes.binding.yaml")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Fatal(err)
	}
	p := gen.Inspect(a, dir)
	if p.CanGenerate() || !has(p, "model.io_error", "notes.binding.yaml", "") {
		t.Fatal(p.String())
	}
	if _, err := p.Apply(); err == nil {
		t.Fatal("symlink plan applied")
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "outside\n" {
		t.Fatal("outside target changed")
	}
}

func TestPreflightStillReportsIndependentMissingFilesWhenLayoutIsInvalid(t *testing.T) {
	a := notes()
	dir := t.TempDir()
	put(t, dir, "model/notes.qnt", []byte("module notesRules {}\n"))
	p := gen.Inspect(a, dir)
	if p.CanGenerate() || p.OK() || !has(p, "model.layout_invalid", "", "") || !has(p, "model.rules_missing", "", "") {
		t.Fatal(p.String())
	}
	blocked, unperformed := false, false
	for _, c := range p.Checks {
		blocked = blocked || c.Name == "generated_files" && c.Status == "blocked"
		unperformed = unperformed || c.Name == "quint_typecheck" && c.Status == "not_checked"
	}
	if !blocked || !unperformed || p.Complete {
		t.Fatalf("blocked scan claimed complete semantic coverage: %+v", p.Checks)
	}
	g, err := gen.Generate(a, dir)
	if err == nil || len(g.Files) != 0 {
		t.Fatal("failed generation exposed partial output")
	}
}
