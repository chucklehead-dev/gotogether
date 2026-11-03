//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package quintcmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAdmissionHelper(t *testing.T) {
	if os.Getenv("GOTOGETHER_ADMISSION_HELPER") != "1" {
		return
	}
	fmt.Println("waiting")
	release, err := acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fmt.Println("admitted")
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
}

func isolatedPool(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
}

func TestAdmissionAcrossProcessesAndOwnerDeath(t *testing.T) {
	isolatedPool(t)
	start := func() (*exec.Cmd, <-chan string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestAdmissionHelper$")
		// Each binary may have its own scratch directory (qtest does).
		// Admission must still coordinate through their shared user cache.
		cmd.Env = append(os.Environ(), "GOTOGETHER_ADMISSION_HELPER=1", "TMPDIR="+t.TempDir())
		pipe, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		lines := make(chan string, 2)
		go func() {
			s := bufio.NewScanner(stdout)
			for s.Scan() {
				lines <- s.Text()
			}
		}()
		t.Cleanup(func() { _ = pipe.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
		select {
		case line := <-lines:
			if line != "waiting" {
				t.Fatalf("helper startup: %q", line)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("helper did not attempt admission")
		}
		return cmd, lines
	}
	ready := func(ch <-chan string) {
		t.Helper()
		select {
		case line := <-ch:
			if line != "admitted" {
				t.Fatalf("helper: %q", line)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("helper did not acquire")
		}
	}
	first, a := start()
	ready(a)
	_, b := start()
	ready(b)
	_, c := start()
	select {
	case line := <-c:
		t.Fatalf("third process acquired occupied pool: %s", line)
	case <-time.After(100 * time.Millisecond):
	}
	// Kernel locks are released on abrupt owner death without removing files.
	if err := first.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	ready(c)
}

func TestAdmissionCancellationAndStartFailure(t *testing.T) {
	isolatedPool(t)
	a, err := acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer a()
	b, err := acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	cmd := Command(ctx, os.Args[0], "", "-test.run=^TestAdmissionHelper$")
	if err := cmd.Start(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued Start: %v", err)
	}
	if cmd.Process != nil {
		t.Fatal("canceled waiter launched a process")
	}
	b()
	missing := Command(t.Context(), filepath.Join(t.TempDir(), "missing"), "")
	if err := missing.Start(); err == nil {
		t.Fatal("missing executable succeeded")
	}
	ctx2, cancel2 := context.WithTimeout(t.Context(), time.Second)
	defer cancel2()
	release, err := acquire(ctx2)
	if err != nil {
		t.Fatalf("failed Start leaked permit: %v", err)
	}
	release()
}

func TestAdmissionRejectsInsecureDirectory(t *testing.T) {
	isolatedPool(t)
	release, err := acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release()
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := filepath.Glob(filepath.Join(cache, "gotogether-quint-slots-v1"))
	if err != nil || len(dirs) != 1 {
		t.Fatalf("pool directories %v: %v", dirs, err)
	}
	if err := os.Chmod(dirs[0], 0755); err != nil {
		t.Fatal(err)
	}
	if release, err := acquire(t.Context()); err == nil {
		release()
		t.Fatal("insecure directory admitted")
	}
}
