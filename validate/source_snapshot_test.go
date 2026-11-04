package validate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

func sourceSnapshotFile(t *testing.T, root, name, contents string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSourceSnapshotPreservesPortableImportsAndHashes(t *testing.T) {
	skip.Quint(t)
	t.Parallel()
	root := t.TempDir()
	original := filepath.Join(root, "original")
	spec := sourceSnapshotFile(t, original, "model/main.qnt", "module main { import helper.* from \"../lib/helper\"\n pure val answer = one + 1 }\n")
	sourceSnapshotFile(t, original, "lib/helper.qnt", "module helper { pure val one = 1 }\n")
	sourceSnapshotFile(t, original, "model/sibling.qnt", "module sibling { pure val unused = 3 }\n")
	guard, err := CaptureModelSources(spec)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "snapshot")
	snapshot, err := guard.Export(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Spec.Path != "model/main.qnt" || len(snapshot.Files) != 3 {
		t.Fatalf("inventory: %+v", snapshot)
	}
	for _, f := range snapshot.Files {
		if filepath.IsAbs(f.Path) || strings.Contains(f.Path, "\\") {
			t.Fatalf("nonportable path: %+v", f)
		}
		hash, err := hashFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil || hash != f.SHA256 {
			t.Fatalf("copied hash: %+v, %s, %v", f, hash, err)
		}
	}
	if err := os.RemoveAll(original); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "relocated")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	cmd := quintcmd.Command(t.Context(), "quint", moved, "typecheck", snapshot.Spec.Path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("relocated sources are not independently usable: %v\n%s", err, out)
	}
}

func TestSourceSnapshotRefusesChangesAndExistingEvidence(t *testing.T) {
	root := t.TempDir()
	spec := sourceSnapshotFile(t, root, "original/main.qnt", "module main { pure val x = 1 }\n")
	guard, err := CaptureModelSources(spec)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "existing")
	marker := sourceSnapshotFile(t, out, "marker", "original evidence")
	if _, err := guard.Export(t.Context(), out); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing directory: %v", err)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "original evidence" {
		t.Fatalf("existing evidence changed: %q, %v", got, err)
	}
	sourceSnapshotFile(t, root, "original/main.qnt", "module main { pure val x = 2 }\n")
	fresh := filepath.Join(root, "fresh")
	if _, err := guard.Export(t.Context(), fresh); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed source: %v", err)
	}
	if _, err := os.Lstat(fresh); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed export published a directory: %v", err)
	}
}

func TestSourceSnapshotRefusesUncapturedSiblingImports(t *testing.T) {
	root := t.TempDir()
	spec := sourceSnapshotFile(t, root, "model/main.qnt", "module main { pure val x = 1 }\n")
	sourceSnapshotFile(t, root, "model/sibling.qnt", "module sibling { import outside.* from \"../lib/outside\" }\n")
	sourceSnapshotFile(t, root, "lib/outside.qnt", "module outside { pure val y = 1 }\n")
	guard, err := CaptureModelSources(spec)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "export")
	if _, err := guard.Export(t.Context(), out); err == nil || !strings.Contains(err.Error(), "absent from the captured inventory") {
		t.Fatalf("uncaptured dependency: %v", err)
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid snapshot published: %v", err)
	}
}

func TestSourceSnapshotMaterializesSymlinkBytes(t *testing.T) {
	root := t.TempDir()
	target := sourceSnapshotFile(t, root, "outside/source.txt", "module main { pure val x = 1 }\n")
	spec := filepath.Join(root, "model/main.qnt")
	if err := os.MkdirAll(filepath.Dir(spec), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, spec); err != nil {
		skip.Optional(t, "symlink: "+err.Error())
	}
	guard, err := CaptureModelSources(spec)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "export")
	snapshot, err := guard.Export(t.Context(), out)
	if err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(out, snapshot.Spec.Path)
	info, err := os.Lstat(copied)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("exported source is not regular: %v, %v", info, err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if hash, err := hashFile(copied); err != nil || hash != snapshot.Spec.SHA256 {
		t.Fatalf("export still depends on symlink target: %s, %v", hash, err)
	}
}

func TestSourceSnapshotCancellationAndAbsoluteImports(t *testing.T) {
	root := t.TempDir()
	spec := sourceSnapshotFile(t, root, "model/main.qnt", "module main { pure val x = 1 }\n")
	guard, err := CaptureModelSources(spec)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out := filepath.Join(root, "absent-parent/export")
	if _, err := guard.Export(ctx, out); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled export: %v", err)
	}
	if _, err := os.Lstat(filepath.Dir(out)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled export created directories: %v", err)
	}
	abs := sourceSnapshotFile(t, root, "absolute.qnt", "module absolute { import external.* from \"/outside/source\" }\n")
	if err := snapshotImports(t.Context(), []File{{Path: abs}}); err == nil || !strings.Contains(err.Error(), "absolute import") {
		t.Fatalf("absolute reference: %v", err)
	}
}

// Export ownership must start at the original checker capture, not at whichever
// bytes happen to be present when replay evidence is later exported.
func TestCapturedModelSourcesKeepsOriginalInventoryAndOwnsGuard(t *testing.T) {
	path := sourceFixture(t)
	b, err := binding.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := captureSource(b)
	if err != nil {
		t.Fatal(err)
	}
	c := &Checker{Binding: b, source: captured}
	guard, err := c.CapturedModelSources()
	if err != nil {
		t.Fatal(err)
	}
	originalHash := captured.files[0].SHA256
	guard.files[0].SHA256 = "caller edit"
	owned, err := c.CapturedModelSources()
	if err != nil || owned.files[0].SHA256 != originalHash {
		t.Fatalf("caller changed checker capture: %+v, %v", owned, err)
	}
	spec := b.SpecPath()
	original, err := os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec, append(append([]byte{}, original...), []byte("\n// different model bytes\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if refreshed, err := c.CapturedModelSources(); err == nil || refreshed != nil {
		t.Fatalf("changed bytes refreshed original source identity: %+v, %v", refreshed, err)
	}
	if err := owned.Verify(); err == nil {
		t.Fatal("previously returned guard accepted different bytes")
	}
	if err := os.WriteFile(spec, original, 0o644); err != nil {
		t.Fatal(err)
	}
	restored, err := c.CapturedModelSources()
	if err != nil || restored.files[0].SHA256 != originalHash {
		t.Fatalf("restored capture: %+v, %v", restored, err)
	}
	for _, missing := range []*Checker{nil, {}, {Binding: b}, {Binding: b, source: &sourceCapture{}}} {
		if sources, err := missing.CapturedModelSources(); err == nil || sources != nil {
			t.Fatalf("uncaptured checker accepted: %+v, %v", sources, err)
		}
	}
}
