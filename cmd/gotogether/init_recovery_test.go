package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

func TestInitSetupRecovery(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("requires POSIX shell")
	}
	for _, kind := range []string{"http", "library"} {
		for _, stage := range []string{"get", "mod"} {
			t.Run(kind+"/"+stage, func(t *testing.T) {
				root := t.TempDir()
				dir := filepath.Join(root, `project ' $(touch INJECTED) ;`)
				bin := filepath.Join(root, "bin")
				os.Mkdir(bin, 0755)
				fake := `#!/bin/sh
printf '%s|%s|' "$PWD" "$GOWORK" >> "$INIT_LOG"
printf '<%s>' "$@" >> "$INIT_LOG"
printf '\n' >> "$INIT_LOG"
if [ "$1" = "$INIT_FAIL" ]; then exit 19; fi
`
				os.WriteFile(filepath.Join(bin, "go"), []byte(fake), 0755)
				log := filepath.Join(root, "calls")
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				t.Setenv("INIT_LOG", log)
				t.Setenv("INIT_FAIL", stage)
				t.Setenv("GOWORK", "unwanted-workspace")
				var out bytes.Buffer
				err := runInit(&out, io.Discard, []string{"-kind", kind, "-dir", dir, "sample"})
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 19 {
					t.Fatalf("lost failure: %v", err)
				}
				if strings.Contains(out.String(), "next:") {
					t.Fatalf("reported success: %s", out.String())
				}
				if _, err := os.Stat(filepath.Join(dir, "sample.go")); err != nil {
					t.Fatal("generated files lost", err)
				}
				edited := filepath.Join(dir, "user-edit.txt")
				os.WriteFile(edited, []byte("preserve"), 0644)
				_, resume, ok := strings.Cut(err.Error(), "\nresume: ")
				if !ok {
					t.Fatal("no recovery", err)
				}
				os.WriteFile(log, nil, 0644)
				t.Setenv("INIT_FAIL", "")
				cmd := exec.Command(shell, "-c", resume)
				cmd.Dir = root
				if b, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("recovery %v: %s\n%s", err, b, resume)
				}
				calls, _ := os.ReadFile(log)
				if !strings.Contains(string(calls), dir+"|off|") || !strings.Contains(string(calls), "<test><-short><-v><./...>") {
					t.Fatalf("wrong recovery: %s", calls)
				}
				if stage == "get" && !strings.Contains(string(calls), "<get><github.com/chucklehead-dev/gotogether@"+cliVersion()+">") {
					t.Fatalf("lost pinned get: %s", calls)
				}
				if stage == "mod" && strings.Contains(string(calls), "<get>") {
					t.Fatalf("repeated finished step: %s", calls)
				}
				if data, _ := os.ReadFile(edited); string(data) != "preserve" {
					t.Fatal("user edit lost")
				}
				if _, err := os.Stat(filepath.Join(root, "INJECTED")); !os.IsNotExist(err) {
					t.Fatal("shell substitution executed")
				}
				if err := runInit(io.Discard, io.Discard, []string{"-dir", dir, "-no-tidy", "sample"}); err == nil || !strings.Contains(err.Error(), "not empty") {
					t.Fatalf("initializer overwrote preserved work: %v", err)
				}
			})
		}
	}
}

func TestInitPreflightAndWriteOnlyRecovery(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{{"-module", "bad module"}, {}} {
		dir := filepath.Join(t.TempDir(), "absent")
		argv := append([]string{"-dir", dir}, args...)
		argv = append(argv, "sample")
		if err := runInit(io.Discard, io.Discard, argv); err == nil {
			t.Fatal("preflight accepted", argv)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("preflight wrote project")
		}
	}
	dir := filepath.Join(t.TempDir(), "write only")
	var out bytes.Buffer
	if err := runInit(&out, io.Discard, []string{"-dir", dir, "-no-tidy", "-module", "local", "sample"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "go get github.com/chucklehead-dev/gotogether@"+cliVersion()) || !strings.Contains(out.String(), "GOWORK=off go mod tidy") {
		t.Fatalf("incomplete write-only instructions: %s", out.String())
	}
}

func TestInitQuotesReplacementGoMod(t *testing.T) {
	repo := filepath.Join(t.TempDir(), `checkout ' with "quotes"`)
	os.Mkdir(repo, 0755)
	os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module github.com/chucklehead-dev/gotogether\n"), 0644)
	dir := filepath.Join(t.TempDir(), "project")
	if err := runInit(io.Discard, io.Discard, []string{"-dir", dir, "-replace", repo, "-no-tidy", "sample"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := modfile.Parse("go.mod", b, nil)
	if err != nil {
		t.Fatalf("invalid quoted go.mod: %v\n%s", err, b)
	}
	if len(m.Replace) != 1 {
		t.Fatalf("replace missing: %s", b)
	}
	got := filepath.Clean(filepath.Join(dir, filepath.FromSlash(m.Replace[0].New.Path)))
	if got != repo {
		t.Fatalf("replacement = %q, want %q", got, repo)
	}
}
