package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestSimulateCanceledRequestIsOperational(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var resultErr error
	code, output := capture(t, func() (int, error) {
		var code int
		code, resultErr = simulate(ctx, []string{"-json", "-binding", "must-not-be-read.yaml"})
		return code, nil
	})
	if code != 2 || !errors.Is(resultErr, context.Canceled) {
		t.Fatalf("exit=%d error=%v output=%s", code, resultErr, output)
	}
	var document validate.Document
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatal(err)
	}
	if document.Verdict != "error" || document.Exit != 2 || document.Error == "" || document.Graph != nil || len(document.Checks) != 0 {
		t.Fatalf("cancellation became model evidence: %s", output)
	}
}

// Invoke the real command dispatch and signal handler in a child test binary.
func TestAnalysisCommandProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MODEL_ANALYSIS_MAIN") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"gotogether"}, os.Args[i+1:]...)
			main()
			return
		}
	}
	t.Fatal("missing command arguments")
}

// A compiler that cannot finish until released makes cancellation observable
// without relying on a slow model or a sleep before sending the signal.
func TestModelAnalysisSignalCancelsCompiler(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal and shell fixture")
	}
	skip.Quint(t)
	quint, err := exec.LookPath("quint")
	if err != nil {
		t.Fatal(err)
	}
	// Isolate admission without moving Quint's installed runtime assets. Its
	// explicit QUINT_HOME override is authoritative, including an empty value.
	quintHome, explicitHome := os.LookupEnv("QUINT_HOME")
	if !explicitHome {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		quintHome = filepath.Join(home, ".quint")
	}
	// Saturate a controlled parent pool: both real CLI children must reach
	// their compiler gates through their own pools, even under unrelated load.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	for range 2 {
		ctx, cancel := context.WithCancel(t.Context())
		holder := quintcmd.Command(ctx, "sleep", "", "3600")
		if err := holder.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		t.Cleanup(func() { cancel(); _ = holder.Wait() })
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	for _, command := range []string{"simulate", "model-diff"} {
		t.Run(command, func(t *testing.T) {
			dir := t.TempDir()
			started, release := filepath.Join(dir, "started"), filepath.Join(dir, "release")
			wrapper := filepath.Join(dir, "quint")
			script := "#!/bin/sh\nif [ \"$1\" = compile ]; then\n: > " + quote(started) + "\nwhile [ ! -f " + quote(release) + " ]; do sleep 0.02; done\nfi\nexec " + quote(quint) + " \"$@\"\n"
			if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			args := []string{"-test.run=^TestAnalysisCommandProcess$", "--", command, "-json", "-quint", wrapper}
			binding := "../../validate/testdata/graph.yaml"
			if command == "simulate" {
				args = append(args, "-graph", "-binding", binding)
			} else {
				args = append(args, "-before", binding, "-after", binding, "-observe", "x")
			}
			cmd := exec.Command(os.Args[0], args...)
			cache := t.TempDir()
			cmd.Env = append(os.Environ(), "GO_WANT_MODEL_ANALYSIS_MAIN=1", "GOTOGETHER_CACHE=off", "GOTOGETHER_REPORT_DIR=",
				"HOME="+cache, "XDG_CACHE_HOME="+cache, "QUINT_HOME="+quintHome)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			finished := false
			t.Cleanup(func() {
				// Even the old, uncancelable implementation can leave its gate.
				_ = os.WriteFile(release, nil, 0o600)
				if !finished {
					_ = cmd.Process.Signal(syscall.SIGTERM)
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						_ = cmd.Process.Kill()
						<-done
					}
				}
			})
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			deadline := time.NewTimer(30 * time.Second)
			defer deadline.Stop()
			for {
				if _, err := os.Stat(started); err == nil {
					break
				}
				select {
				case err := <-done:
					finished = true
					t.Fatalf("command exited before compiler gate: %v\n%s\n%s", err, &stdout, &stderr)
				case <-deadline.C:
					t.Fatal("command did not reach compiler gate")
				case <-ticker.C:
				}
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				finished = true
				exit, ok := errors.AsType[*exec.ExitError](err)
				if !ok || exit.ExitCode() != 2 {
					t.Fatalf("signal did not produce operational exit2: %v\n%s\n%s", err, &stdout, &stderr)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("canceled command remained blocked in evaluator compilation")
			}
			var document validate.Document
			if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
				t.Fatalf("no final machine report: %v\n%s\n%s", err, &stdout, &stderr)
			}
			if document.Verdict != "error" || document.Exit != 2 || !strings.Contains(document.Error, "context canceled") || document.Graph != nil || len(document.Checks) != 0 {
				t.Fatalf("signal became model evidence: %s", &stdout)
			}
		})
	}
}
