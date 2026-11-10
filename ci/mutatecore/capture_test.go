package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Exercise the executed capture shim, not a reconstructed overlay manifest.
// qweave may remove its originals after Go exits; the retained payload must
// continue to identify precisely the bytes the command received.
func TestOwnedGoCaptureRetainsActualOverlayAndRejectsDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("builds owned command capture helper")
	}
	dir := t.TempDir()
	helper := filepath.Join(dir, "mutatecore")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", helper, ".")
	build.Env = supervisorFixtureEnv(nil)
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	capture := filepath.Join(dir, "captured")
	if err := os.Mkdir(capture, 0700); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(dir, "generated source.go")
	want := []byte("package fixture\nconst Generated=1\n")
	if err := os.WriteFile(payload, want, 0600); err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(dir, "overlay.json")
	raw, _ := json.Marshal(map[string]any{"Replace": map[string]string{"/logical/source.go": payload, "/logical/deleted.go": ""}})
	if err := os.WriteFile(overlay, raw, 0600); err != nil {
		t.Fatal(err)
	}
	realGo, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), helper, "--capture-go", capture, realGo, "test", "-overlay="+overlay)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("capture: %v\n%s", err, b)
	}
	if err := os.Remove(payload); err != nil {
		t.Fatal(err)
	}
	records, err := captureManifest(capture)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || len(records[0].Inputs) != 2 {
		t.Fatalf("manifest=%+v", records)
	}
	var stored string
	for _, input := range records[0].Inputs {
		if input.Source == "/logical/deleted.go" && !input.Deleted {
			t.Fatal("overlay deletion omitted")
		}
		if input.Source == "/logical/source.go" {
			stored = input.File.Path
		}
	}
	got, err := os.ReadFile(stored)
	if err != nil || string(got) != string(want) {
		t.Fatalf("captured bytes=%q err=%v", got, err)
	}
	if err := os.WriteFile(stored, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureManifest(capture); err == nil {
		t.Fatal("changed generated build input accepted")
	}
}
