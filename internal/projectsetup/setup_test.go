package projectsetup

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanFailureAndExecutableContinuation(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("requires POSIX shell")
	}
	for _, failed := range []string{"get", "mod", "run"} {
		t.Run(failed, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, `project ' space $(touch INJECTED) ;`)
			if err := os.Mkdir(dir, 0755); err != nil {
				t.Fatal(err)
			}
			edited := filepath.Join(dir, "user.txt")
			os.WriteFile(edited, []byte("keep user work"), 0644)
			bin := filepath.Join(root, "bin")
			os.Mkdir(bin, 0755)
			script := `#!/bin/sh
printf '%s|%s|' "$PWD" "$GOWORK" >> "$SETUP_LOG"
printf '<%s>' "$@" >> "$SETUP_LOG"
printf '\n' >> "$SETUP_LOG"
if [ "$1" = "$SETUP_FAIL" ]; then exit 17; fi
`
			if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(root, "calls")
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("SETUP_LOG", log)
			t.Setenv("SETUP_FAIL", failed)
			t.Setenv("GOWORK", "unwanted-workspace")
			p := Plan{Label: "new project", Dir: dir, Steps: [][]string{{"get", `example.com/toolkit@v1.2.3`}, {"mod", "tidy"}, {"run", "./cmd/app", "model"}}}
			err := p.Run(io.Discard)
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 17 {
				t.Fatalf("lost subprocess cause: %v", err)
			}
			from := map[string]int{"get": 0, "mod": 1, "run": 2}[failed]
			calls, _ := os.ReadFile(log)
			if got := strings.Count(string(calls), "\n"); got != from+1 {
				t.Fatalf("continued after failure: %s", calls)
			}
			if !strings.Contains(err.Error(), "files retained") || !strings.Contains(err.Error(), p.Resume(from)) {
				t.Fatalf("missing recovery: %v", err)
			}
			os.WriteFile(log, nil, 0644)
			t.Setenv("SETUP_FAIL", "")
			command := exec.Command("sh", "-c", p.Resume(from))
			command.Dir = root
			if out, err := command.CombinedOutput(); err != nil {
				t.Fatalf("resume: %v\n%s\n%s", err, out, p.Resume(from))
			}
			calls, _ = os.ReadFile(log)
			lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
			if len(lines) != len(p.Steps)-from+1 {
				t.Fatalf("wrong remaining stages: %s", calls)
			}
			for _, line := range lines {
				if !strings.HasPrefix(line, dir+"|off|") {
					t.Fatalf("wrong working directory/workspace: %s", line)
				}
			}
			if !strings.HasSuffix(lines[len(lines)-1], "<test><-short><-v><./...>") {
				t.Fatalf("verification missing: %s", calls)
			}
			if _, err := os.Stat(filepath.Join(root, "INJECTED")); !os.IsNotExist(err) {
				t.Fatal("directory executed shell substitution")
			}
			os.WriteFile(log, nil, 0644)
			if err := p.Run(io.Discard); err != nil {
				t.Fatal(err)
			}
			successful, _ := os.ReadFile(log)
			if strings.Count(string(successful), "\n") != 3 || strings.Contains(string(successful), "<test>") {
				t.Fatalf("Run executed verification: %s", successful)
			}
			if data, _ := os.ReadFile(edited); string(data) != "keep user work" {
				t.Fatal("user work altered")
			}
		})
	}
}

func TestPreflightAndGoModPaths(t *testing.T) {
	for _, path := range []string{"local", "example.com/project", "example.com/project/v2", "example.com/project/v10", "gopkg.in/yaml.v2", "gopkg.in/yaml.v1", "gopkg.in/yaml.v0"} {
		if err := CheckModule(path); err != nil {
			t.Errorf("valid %q: %v", path, err)
		}
	}
	for _, path := range []string{"", "bad path", "a\nb", "go", "toolchain", "../relative", "example.com/project/v1", "example.com/project/v01", "example.com/project/v3.0", "gopkg.in/yaml"} {
		if err := CheckModule(path); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
	absent := filepath.Join(t.TempDir(), "absent")
	if err := CheckDestination(absent); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("preflight wrote destination")
	}
	empty := t.TempDir()
	if err := CheckDestination(empty); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(empty, "existing")
	os.WriteFile(file, nil, 0644)
	if CheckDestination(empty) == nil || CheckDestination(file) == nil {
		t.Fatal("accepted occupied destination")
	}
	t.Setenv("PATH", t.TempDir())
	if CheckGo() == nil {
		t.Fatal("accepted missing Go")
	}
	if got := ModulePath(`../checkout with "quote"`); got != `"../checkout with \"quote\""` {
		t.Fatalf("unquoted go.mod path: %s", got)
	}
}
