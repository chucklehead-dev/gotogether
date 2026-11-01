package binding

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCapturesParsedBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binding.yaml")
	data := []byte("spec: model.qnt\nmodule: model\n# exact source bytes\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("spec: replacement.qnt\nmodule: replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := b.SHA256(), fmt.Sprintf("%x", sha256.Sum256(data)); got != want {
		t.Fatalf("parsed source digest = %s, want %s", got, want)
	}
}
