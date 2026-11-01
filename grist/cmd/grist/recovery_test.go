package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fakeSetupGo(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
printf '%s|%s|%s\n' "$PWD" "$GOWORK" "$*" >> "$GRIST_SETUP_LOG"
if [ "$1" = "$GRIST_SETUP_FAIL" ]; then
 echo 'setup fixture failure' >&2
 exit 19
fi
`
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "go.log")
	t.Setenv("PATH", bin)
	t.Setenv("GRIST_SETUP_LOG", log)
	t.Setenv("GOWORK", "/a/parent/workspace/go.work")
	return log
}

func TestNewSetupFailureCanResumeWithoutRewriting(t *testing.T) {
	for _, failed := range []string{"get", "mod", "run"} {
		t.Run(failed, func(t *testing.T) {
			log := fakeSetupGo(t)
			t.Setenv("GRIST_SETUP_FAIL", failed)
			dir := filepath.Join(t.TempDir(), "new app's files")
			var out, diagnostics bytes.Buffer
			err := run([]string{"new", "-dir", dir, "inventory"}, &out, &diagnostics)
			var exit *exec.ExitError
			if err == nil || !errors.As(err, &exit) || exit.ExitCode() != 19 || !strings.Contains(err.Error(), "project files retained in "+dir) || !strings.Contains(diagnostics.String(), "setup fixture failure") {
				t.Fatalf("failure lost cause/project/output: %v; %s", err, &diagnostics)
			}
			_, resume, ok := strings.Cut(err.Error(), "\nresume: ")
			if !ok {
				t.Fatalf("missing executable continuation: %v", err)
			}
			if strings.Contains(resume, "grist new") {
				t.Fatalf("resume would initialize again: %s", resume)
			}
			path := filepath.Join(dir, "commands.go")
			const edit = "// user edits retained during setup recovery\n"
			if err := os.WriteFile(path, []byte(edit), 0644); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("GRIST_SETUP_FAIL", "")
			cmd := exec.Command("/bin/sh", "-c", resume)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("resume failed: %v\n%s\n%s", err, resume, output)
			}
			after, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(after[len(before):])), "\n")
			want := []string{"get github.com/chucklehead-dev/gotogether/grist@" + selfVersion(), "mod tidy", "run ./cmd/inventory model", "test -short -v ./..."}
			from := map[string]int{"get": 0, "mod": 1, "run": 2}[failed]
			initial := strings.Split(strings.TrimSpace(string(before)), "\n")
			if len(initial) != from+1 {
				t.Fatalf("setup stages before failure: %v", initial)
			}
			for i, line := range initial {
				if line != dir+"|off|"+want[i] {
					t.Errorf("initial setup cwd/env/args: %q", line)
				}
			}
			if len(lines) != len(want[from:]) {
				t.Fatalf("incorrect remaining stages: %v", lines)
			}
			for i, line := range lines {
				if line != dir+"|off|"+want[from+i] {
					t.Errorf("resume cwd/env/args: %q, want stage %q", line, want[from+i])
				}
			}
			if data, _ := os.ReadFile(path); string(data) != edit {
				t.Fatal("resume rewrote user edits")
			}
			if err := run([]string{"new", "-dir", dir, "inventory"}, &out, &diagnostics); err == nil || !strings.Contains(err.Error(), "not empty") {
				t.Fatalf("overwrite guard lost after failure: %v", err)
			}
		})
	}
}

func TestNewPreflightAndWriteOnly(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, module := range []string{"bad module", "example.com/../app", "go", "toolchain"} {
		dir := filepath.Join(t.TempDir(), "absent")
		var out bytes.Buffer
		if err := run([]string{"new", "-dir", dir, "-module", module, "-no-generate", "inventory"}, &out, &out); err == nil {
			t.Fatalf("invalid module accepted: %q", module)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("invalid module wrote files: %v", err)
		}
	}
	dir := filepath.Join(t.TempDir(), "absent")
	var out bytes.Buffer
	if err := run([]string{"new", "-dir", dir, "inventory"}, &out, &out); err == nil || !strings.Contains(err.Error(), "Go is required") {
		t.Fatalf("missing Go: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("missing Go left files: %v", err)
	}
	if err := run([]string{"new", "-dir", dir, "-no-generate", "inventory"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"GOWORK=off go get github.com/chucklehead-dev/gotogether/grist@" + selfVersion(), "GOWORK=off go mod tidy", "go run ./cmd/inventory model", "go test -short -v ./..."} {
		if !strings.Contains(text, want) {
			t.Errorf("write-only next steps missing %q: %s", want, text)
		}
	}
}

func TestNewQuotesReplacePaths(t *testing.T) {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Skip("Go unavailable for go.mod parsing")
	}
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	repo := filepath.Join(root, "toolkit's quoted \"checkout\"")
	if err := os.MkdirAll(filepath.Join(repo, "grist"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"go.mod", "grist/go.mod"} {
		if err := os.WriteFile(filepath.Join(repo, path), []byte("module example.com/stub\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(root, "new app's module")
	var out bytes.Buffer
	if err := run([]string{"new", "-dir", dir, "-replace", repo, "-no-generate", "inventory"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	// Ask Go itself to parse both quoted replacement targets, without resolving
	// dependencies or using the enclosing workspace.
	cmd := exec.Command(goBinary, "mod", "edit", "-json")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("invalid quoted go.mod: %v\n%s", err, output)
	}
	if strings.Contains(out.String(), "go get") {
		t.Fatalf("local checkout continuation downloads toolkit: %s", &out)
	}
}
