package validate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

func sourceFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"account.yaml", "account.qnt"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "account.yaml")
}

func TestSourceCheckerRefusesChangedDependencyAndAllowsRestore(t *testing.T) {
	skip.Quint(t)
	path := sourceFixture(t)
	c, err := NewChecker(path, Options{NoITF: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// The conservative inventory includes sibling sources, just as both caches do.
	sibling := filepath.Join(filepath.Dir(path), "new.qnt")
	if err := os.WriteFile(sibling, []byte("module new {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rep, err := c.Check(context.Background(), nil); err == nil || rep != nil || !strings.Contains(err.Error(), sibling) {
		t.Fatalf("reused checker accepted changed dependency inventory: %v, %v", rep, err)
	}
	if err := os.Remove(sibling); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Check(context.Background(), nil); err != nil {
		t.Fatalf("exact source restoration should permit reuse: %v", err)
	}
}

func TestSourceManifestCapturesEffectiveInstanceAndOwnsItsCopy(t *testing.T) {
	path := sourceFixture(t)
	b, err := binding.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	source, err := captureSource(b)
	if err != nil {
		t.Fatal(err)
	}
	c := &Checker{Binding: b, source: source}
	r := &Result{}
	c.captureReport(&Report{Actors: []*Result{r}})
	model := r.SourceModel()
	model.Spec.SHA256 = "caller edit"
	model.Constants["LIMIT"] = "1"
	m := &Manifest{}
	if err := m.setBinding(b); err != nil {
		t.Fatal(err)
	}
	if err := m.matchSourceResults([]*Result{r}, nil); err != nil {
		t.Fatalf("public copy changed private capture: %v", err)
	}
	b.Constants["LIMIT"] = "1"
	if err := m.setBinding(b); err != nil {
		t.Fatal(err)
	}
	if err := m.matchSourceResults([]*Result{r}, nil); err == nil {
		t.Fatal("manifest accepted an instance changed after judgment")
	}
}

func TestSourceCaptureRefusesUntrackedDefsImports(t *testing.T) {
	path := sourceFixture(t)
	b, err := binding.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	b.Defs = `import extra.* from "./extra"`
	if _, err := captureSource(b); err == nil || !strings.Contains(err.Error(), "move file imports into the model") {
		t.Fatalf("untracked executable dependency accepted: %v", err)
	}
}

func TestSourceCheckerRefusesUnrecordedBindingEdits(t *testing.T) {
	path := sourceFixture(t)
	b, err := binding.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	source, err := captureSource(b)
	if err != nil {
		t.Fatal(err)
	}
	c := &Checker{Binding: b, source: source}
	b.Defs += "\n pure val unrecorded = 42\n"
	if err := c.VerifySource(); err == nil || !strings.Contains(err.Error(), "execution configuration changed") {
		t.Fatalf("unrecorded executable binding edit accepted: %v", err)
	}
}
