// Package apisurface keeps the core's exported API in api/core.txt: every
// package's `go doc -short`, so a change to what users can call shows in
// review, and, until tags give apidiff a baseline, is never made by
// accident.
package apisurface

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const file = "../../api/core.txt"

func surface(t *testing.T) []byte {
	t.Helper()
	cmd := exec.Command("go", "list", "./...")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	for _, pkg := range strings.Fields(string(out)) {
		if strings.Contains(pkg, "/internal/") || strings.HasSuffix(pkg, "/internal") || strings.Contains(pkg, "/cmd/") {
			continue
		}
		doc := exec.Command("go", "doc", "-short", pkg)
		doc.Dir = "../.."
		d, err := doc.Output()
		if err != nil {
			t.Fatalf("go doc %s: %v", pkg, err)
		}
		b.WriteString("== " + pkg + "\n")
		b.Write(d)
		b.WriteString("\n")
	}
	return b.Bytes()
}

// The exported API is the recorded one. After a deliberate change:
// GOTOGETHER_UPDATE_API=1 go test ./internal/apisurface/, and review the
// diff of api/core.txt with the change.
func TestAPISurface(t *testing.T) {
	got := surface(t)
	if os.Getenv("GOTOGETHER_UPDATE_API") == "1" {
		if err := os.WriteFile(file, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v: write it with GOTOGETHER_UPDATE_API=1 go test ./internal/apisurface/", err)
	}
	if !bytes.Equal(got, want) {
		gl, wl := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
		var diff []string
		seen := map[string]bool{}
		for _, l := range wl {
			seen[l] = true
		}
		for _, l := range gl {
			if !seen[l] {
				diff = append(diff, "+ "+l)
			}
		}
		now := map[string]bool{}
		for _, l := range gl {
			now[l] = true
		}
		for _, l := range wl {
			if !now[l] {
				diff = append(diff, "- "+l)
			}
		}
		t.Errorf("the core's exported API changed:\n%s\nIf deliberate: GOTOGETHER_UPDATE_API=1 go test ./internal/apisurface/, and commit api/core.txt with the change (and a CHANGELOG line)", strings.Join(diff, "\n"))
	}
}
