//go:build linux

package model

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// This is local process/socket accounting, not authentication against hostile
// same-user processes. The selected managed installation and local OS/network
// namespace must remain stable. Missing/unreadable procfs only disables reuse.
func observeNativeBackend(ctx context.Context, leader *os.Process, endpoint string, expected nativeBackendIdentity) func() bool {
	done, stop := make(chan struct{}), make(chan struct{})
	observed := false
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			// The retained process handle prevents a reaped/reused leader from
			// authorizing unrelated later members of the same numeric process group.
			if nativeBackendOwned(leader, endpoint, expected) {
				observed = true
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return sync.OnceValue(func() bool { close(stop); <-done; return observed })
}

func nativeBackendOwned(leader *os.Process, endpoint string, expected nativeBackendIdentity) bool {
	if !nativeLeaderAlive(leader) {
		return false
	}
	_, portText, err := net.SplitHostPort(endpoint)
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return false
	}
	sockets := map[string]bool{}
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(table)
		if err != nil {
			return false
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			_, hexPort, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			actual, err := strconv.ParseUint(hexPort, 16, 16)
			if err == nil && int(actual) == port {
				sockets["socket:["+fields[9]+"]"] = true
			}
		}
	}
	if len(sockets) == 0 {
		return false
	}
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, entry := range processes {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		root := filepath.Join("/proc", entry.Name())
		stat, err := os.ReadFile(filepath.Join(root, "stat"))
		if err != nil {
			continue
		}
		at := strings.LastIndex(string(stat), ") ")
		if at < 0 {
			continue
		}
		fields := strings.Fields(string(stat)[at+2:])
		if len(fields) < 3 || fields[0] == "Z" || fields[2] != strconv.Itoa(leader.Pid) {
			continue
		}
		exe, err := os.Readlink(filepath.Join(root, "exe"))
		if err != nil || exe != expected.java {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
		jar := ""
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-jar" {
				jar = args[i+1]
				break
			}
		}
		jar, err = filepath.EvalSymlinks(jar)
		if err != nil || filepath.Clean(jar) != filepath.Clean(expected.jar) {
			continue
		}
		descriptors, err := os.ReadDir(filepath.Join(root, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range descriptors {
			target, err := os.Readlink(filepath.Join(root, "fd", fd.Name()))
			if err == nil && sockets[target] {
				delete(sockets, target)
			}
		}
	}
	return len(sockets) == 0 && nativeLeaderAlive(leader)
}

func nativeLeaderAlive(leader *os.Process) bool {
	return leader != nil && leader.Signal(syscall.Signal(0)) == nil
}
