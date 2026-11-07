package validate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// Returning a warm model must still admit the selected live CLI. Replacing a
// wrapper's delegate must not turn the memoized model into a version bypass.
func TestWarmModelRejectsUnsupportedRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper control")
	}
	skip.Quint(t)
	// Force the positive setup to execute the real typechecker, then exercise
	// the process-local model memo. A prior run's disk artifact is not setup.
	t.Setenv("GOTOGETHER_CACHE", "off")
	real, err := exec.LookPath("quint")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "quint")
	quote := "'" + strings.ReplaceAll(real, "'", "'\\''") + "'"
	body := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = --version ]; then cat \"$0.version\"; exit 0; fi\necho work >> \"$0.work\"\nexec %s \"$@\"\n", quote)
	if err := os.WriteFile(bin, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin+".version", []byte("0.33.1"), 0600); err != nil {
		t.Fatal(err)
	}
	spec, err := filepath.Abs("testdata/account.qnt")
	if err != nil {
		t.Fatal(err)
	}
	model, err := loadModel(t.Context(), bin, spec, "account")
	if err != nil || model == nil {
		t.Fatalf("native supported positive: %v", err)
	}
	before, err := os.ReadFile(bin + ".work")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin+".version", []byte("0.33.0"), 0600); err != nil {
		t.Fatal(err)
	}
	model, err = loadModel(t.Context(), bin, spec, "account")
	if err == nil || model != nil || !strings.Contains(err.Error(), "unsupported Quint version \"0.33.0\"") {
		t.Fatalf("warm model bypassed admission: model=%v error=%v", model != nil, err)
	}
	after, err := os.ReadFile(bin + ".work")
	if err != nil || string(after) != string(before) {
		t.Fatalf("unsupported CLI executed model work: %q -> %q (%v)", before, after, err)
	}
}
