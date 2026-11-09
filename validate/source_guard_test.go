package validate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceGuardFilesOwnsInventoryAndDetectsNewSource(t *testing.T) {
	dir := t.TempDir()
	spec := sourceSnapshotFile(t, dir, "main.qnt", "module main { pure val x = 1 }\n")
	sourceSnapshotFile(t, dir, "sibling.qnt", "module sibling { pure val y = 1 }\n")
	guard, err := CaptureModelSources(spec)
	if err != nil {
		t.Fatal(err)
	}
	files, err := guard.Files()
	if err != nil || len(files) != 2 {
		t.Fatalf("inventory=%v error=%v", files, err)
	}
	files[0].Path = "caller edit"
	files[1].SHA256 = "caller edit"
	again, err := guard.Files()
	if err != nil || again[0].Path == "caller edit" || again[1].SHA256 == "caller edit" {
		t.Fatalf("aliased inventory: %v %v", again, err)
	}
	newFile := sourceSnapshotFile(t, dir, "new.qnt", "module new { pure val z = 1 }\n")
	if _, err := guard.Files(); err == nil {
		t.Fatal("new dependency inventory accepted")
	}
	if err := os.Remove(newFile); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sibling.qnt"), []byte("module sibling { pure val y = 2 }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Files(); err == nil {
		t.Fatal("changed dependency accepted")
	}
	var absent *SourceGuard
	if _, err := absent.Files(); err == nil {
		t.Fatal("missing guard supplied an identity")
	}
}
