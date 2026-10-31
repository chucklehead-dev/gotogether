package convert

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRulesCaptureParsedBytes(t *testing.T) {
	raw := []byte(rules)
	want := fmt.Sprintf("%x", sha256.Sum256(raw))
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.SHA256() != want {
		t.Fatalf("parsed digest = %q, want %q", parsed.SHA256(), want)
	}
	path := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if loaded.SHA256() != want {
		t.Fatalf("loaded digest changed: %q", loaded.SHA256())
	}
	if got := (&Rules{}).SHA256(); got != "" {
		t.Fatalf("uncaptured rules digest = %q", got)
	}
}
