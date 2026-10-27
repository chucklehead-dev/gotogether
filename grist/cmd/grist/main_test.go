package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// grist new writes a module that is green on its first run: the files,
// go mod tidy, the app's model command generating its skeleton and
// bindings, then `go test -short ./...` in it (the simulation's claims,
// its mutant caught, the real tier, the generated files' witness). Full
// mode: it builds a module and its mutant (about two CPU minutes).
func TestNewIsGreen(t *testing.T) {
	if testing.Short() {
		t.Skip("full mode: builds and tests a new module")
	}
	if _, err := exec.LookPath("quint"); err != nil {
		if os.Getenv("GOTOGETHER_STRICT") == "1" {
			t.Fatal("quint is not on PATH (GOTOGETHER_STRICT=1 makes this a failure)")
		}
		t.Skip("quint is not on PATH")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "item-store")
	var out bytes.Buffer
	if err := run([]string{"new", "-dir", dir, "-replace", repo, "-module", "example.com/itemstore", "item-store"}, &out, &out); err != nil {
		t.Fatalf("grist new: %v\n%s", err, out.String())
	}
	for _, f := range []string{"go.mod", "app.go", "cmd/item-store/main.go", "model/item-store.qnt", "model/item-store_gen.qnt", "model/grist.qnt",
		"item-store.binding.yaml", "item-store.calls.binding.yaml", "sim_test.go", "real_test.go", "mutants_test.go", "testdata/mutants/noCapacity/commands.go", "run-demo.sh", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("no %s: %v", f, err)
		}
	}
	cmd := exec.Command("go", "test", "-short", "-count=1", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test ./... in the new module: %v\n%s", err, b)
	}
	if fmtOut, _ := exec.Command("gofmt", "-l", dir).CombinedOutput(); len(bytes.TrimSpace(fmtOut)) > 0 {
		t.Errorf("gofmt -l: %s", fmtOut)
	}
	t.Logf("%s", strings.TrimSpace(string(b)))
}

// grist new refuses a name that is no app's, and a directory in use.
func TestNewRefuses(t *testing.T) {
	var out bytes.Buffer
	for _, args := range [][]string{{"new", "Item"}, {"new", "item-"}, {"new", "to"}, {"new"}, {"other"}} {
		if err := run(args, &out, &out); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x"), nil, 0o644)
	if err := run([]string{"new", "-dir", dir, "-no-generate", "app"}, &out, &out); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("a directory in use: %v", err)
	}
	// an app named grist would have the vendored library's model file
	if err := run([]string{"new", "-dir", filepath.Join(t.TempDir(), "g"), "-no-generate", "grist"}, &out, &out); err == nil || !strings.Contains(err.Error(), "library") {
		t.Errorf("an app named grist: %v, want refused", err)
	}
	fresh := filepath.Join(t.TempDir(), "app")
	if err := run([]string{"new", "-dir", fresh, "-no-generate", "app"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(fresh, "go.mod")); err != nil || !strings.HasPrefix(string(b), "module example.com/app\n") {
		t.Errorf("go.mod %q (%v)", b, err)
	}
}
