//go:build linux

package model

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNativeBackendProcess(t *testing.T) {
	if os.Getenv("GOTOGETHER_TEST_BACKEND_PROCESS") != "1" {
		return
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(listener.Addr().String())
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		connection.Close()
	}
}

func nativeBackendFixture(t *testing.T) (*exec.Cmd, string, nativeBackendIdentity) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	jar := filepath.Join(t.TempDir(), "fixture.jar")
	if err := os.WriteFile(jar, []byte("test fixture, not Java"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestNativeBackendProcess$", "--", "-jar", jar)
	cmd.Env = append(os.Environ(), "GOTOGETHER_TEST_BACKEND_PROCESS=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	endpoint, err := bufio.NewReader(output).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return cmd, strings.TrimSpace(endpoint), nativeBackendIdentity{java: executable, jar: jar}
}

func TestNativeBackendOwnershipRequiresActualSocketGroupAndJar(t *testing.T) {
	backend, endpoint, expected := nativeBackendFixture(t)
	if !nativeBackendOwned(backend.Process, endpoint, expected) {
		t.Fatal("owned listening process was not accounted")
	}
	stop := observeNativeBackend(t.Context(), backend.Process, endpoint, expected)
	first, second := stop(), stop()
	if !first || !second {
		t.Fatal("owned observation was lost or stop was not idempotent")
	}
	foreign, _, _ := nativeBackendFixture(t)
	if nativeBackendOwned(foreign.Process, endpoint, expected) {
		t.Fatal("external server attributed to an unrelated owned group")
	}
	wrong := expected
	wrong.jar = filepath.Join(t.TempDir(), "different.jar")
	if err := os.WriteFile(wrong.jar, []byte("same name is not identity"), 0600); err != nil {
		t.Fatal(err)
	}
	if nativeBackendOwned(backend.Process, endpoint, wrong) {
		t.Fatal("wrong jar was attributed to captured backend")
	}
	wrong = expected
	wrong.java = "/unaccounted/java"
	if nativeBackendOwned(backend.Process, endpoint, wrong) {
		t.Fatal("wrong executable was attributed to captured backend")
	}
	if err := backend.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = backend.Wait()
	if nativeBackendOwned(backend.Process, endpoint, expected) {
		t.Fatal("reaped leader authorized a numeric process group")
	}
}

func TestNativeBackendObserverAndReservationCancel(t *testing.T) {
	backend, endpoint, expected := nativeBackendFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stop := observeNativeBackend(ctx, backend.Process, endpoint, expected)
	if stop() {
		t.Fatal("canceled observer reported new ownership")
	}
	ctx, cancel = context.WithCancel(t.Context())
	reservation, err := reserveNativeBackend(ctx, expected, observeNativeBackend)
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.close()
	accepted := make(chan error, 1)
	go func() { _, err := reservation.listener.Accept(); accepted <- err }()
	cancel()
	select {
	case err := <-accepted:
		if err == nil {
			t.Fatal("canceled reservation accepted a connection")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled reservation remained open")
	}
}
