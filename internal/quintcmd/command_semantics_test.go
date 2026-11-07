package quintcmd_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
)

// The generic exec fixture explicitly implements Quint's inspection protocol.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("0.33.1")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCommandIOHelper(t *testing.T) {
	mode := os.Getenv("GOTOGETHER_COMMAND_IO_HELPER")
	if mode == "" {
		return
	}
	if mode == "hold" {
		fmt.Fprintln(os.Stdout, "ready")
		var b [1]byte
		_, _ = os.Stdin.Read(b[:])
		os.Exit(0)
	}
	fmt.Fprint(os.Stdout, "stdout")
	fmt.Fprint(os.Stderr, "stderr")
	if mode == "fail" {
		os.Exit(7)
	}
	os.Exit(0)
}

func ioCommand(t *testing.T, ctx context.Context, mode string) *quintcmd.Cmd {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := quintcmd.Command(ctx, bin, "", "-test.run=^TestCommandIOHelper$")
	// Only these controlled helper subprocesses omit race's default one-second
	// exit delay. The extra --version process must not consume the five-second
	// permit-leak deadline; parent/child race instrumentation stays enabled.
	c.Env = append(os.Environ(), "GOTOGETHER_COMMAND_IO_HELPER="+mode,
		"GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	return c
}

func TestCommandOutputSemantics(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	for _, mode := range []string{"ok", "fail"} {
		t.Run(mode, func(t *testing.T) {
			c := ioCommand(t, t.Context(), mode)
			out, err := c.Output()
			if string(out) != "stdout" || c.ProcessState == nil {
				t.Fatalf("Output = %q; state = %v", out, c.ProcessState)
			}
			if mode == "ok" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var ee *exec.ExitError
				if !errors.As(err, &ee) || ee.ExitCode() != 7 || string(ee.Stderr) != "stderr" {
					t.Fatalf("Output error = %#v, want exit 7 with captured stderr", err)
				}
			}
		})
	}
	c := ioCommand(t, t.Context(), "fail")
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	var ee *exec.ExitError
	if string(out) != "stdout" || stderr.String() != "stderr" || !errors.As(err, &ee) || len(ee.Stderr) != 0 {
		t.Fatalf("caller-owned stderr: stdout=%q stderr=%q error=%#v", out, stderr.String(), err)
	}
	c = ioCommand(t, t.Context(), "ok")
	out, err = c.CombinedOutput()
	if err != nil || string(out) != "stdoutstderr" {
		t.Fatalf("CombinedOutput = %q, %v", out, err)
	}
	for _, method := range []string{"Output", "CombinedOutput"} {
		c = ioCommand(t, t.Context(), "ok")
		c.Stdout = &bytes.Buffer{}
		if method == "Output" {
			_, err = c.Output()
		} else {
			_, err = c.CombinedOutput()
		}
		if err == nil || !strings.Contains(err.Error(), "Stdout already set") || c.Process != nil {
			t.Fatalf("%s with Stdout: %v, process %v", method, err, c.Process)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := ioCommand(t, ctx, "ok").Run(); err != nil {
		t.Fatalf("permit unavailable after invalid output configuration: %v", err)
	}
}

func TestCommandAdmissionCancellationAndReuse(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	// Two blocked children occupy both slots. Their stdin writers let cleanup
	// finish them without timing assumptions or long-lived background children.
	for i := range 2 {
		c := ioCommand(t, t.Context(), "hold")
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		c.Stdin = r
		if err := c.Start(); err != nil {
			_ = r.Close()
			_ = w.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = w.Close(); _ = c.Wait(); _ = r.Close() })
		if err := c.Start(); err == nil {
			t.Fatal("second Start succeeded")
		}
		if i == 0 {
			_, err = c.Output()
		} else {
			_, err = c.CombinedOutput()
		}
		if err == nil {
			t.Fatal("output method reran a started command")
		}
	}
	// Merely constructing another command must not need an available permit.
	_ = ioCommand(t, t.Context(), "ok")
	ctx, cancel := context.WithCancel(t.Context())
	queued := ioCommand(t, ctx, "ok")
	done := make(chan error, 1)
	go func() { done <- queued.Run() }()
	select {
	case err := <-done:
		t.Fatalf("third command bypassed the occupied slots: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || queued.Process != nil {
			t.Fatalf("queued cancellation = %v, process %v", err, queued.Process)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued cancellation did not return")
	}
}

func TestCommandFailuresReleaseAdmission(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	// More failures than the capacity expose a leaked permit.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for range 3 {
		c := quintcmd.Command(ctx, filepath.Join(t.TempDir(), "missing"), "")
		if err := c.Run(); err == nil {
			t.Fatal("missing executable ran")
		}
	}
	for range 3 {
		c := ioCommand(t, ctx, "fail")
		if err := c.Run(); err == nil {
			t.Fatal("nonzero exit succeeded")
		}
		if err := c.Wait(); err == nil {
			t.Fatal("second Wait succeeded")
		}
	}
	if err := ioCommand(t, ctx, "ok").Run(); err != nil {
		t.Fatalf("permit unavailable after failed commands: %v", err)
	}
}
