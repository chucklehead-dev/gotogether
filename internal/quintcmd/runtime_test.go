package quintcmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

func runtimePool(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
}

func TestSupportedVersions(t *testing.T) {
	for _, v := range []string{"0.33.1", "v0.33.1", "0.33.1+build.2", "0.33.2", "0.34.0", "1.0.0"} {
		if !Supported(v) {
			t.Errorf("rejected stable %q", v)
		}
	}
	for _, v := range []string{"", "unknown", "0.32.9", "0.33.0", "0.33.1-rc.1", "0.34.0-beta.1", "0.33", "0.033.1", "0.33.1\n0.34.0"} {
		if Supported(v) {
			t.Errorf("admitted %q", v)
		}
	}
}

func versionFixture(t *testing.T, version string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake CLI")
	}
	bin := filepath.Join(t.TempDir(), "quint")
	script := `#!/bin/sh
if [ "$1" = --version ]; then
 echo probe >> "$0.probes"
 if [ "${PROBE_HOLD:-}" = 1 ]; then : > "$0.started"; exec sleep 30; fi
 if [ "${PROBE_FROM_CWD:-}" = 1 ]; then cat version; else printf '%s\n' "${PROBE_VERSION:-$(cat "$0.version")}"; fi
 if [ "${PROBE_WARNING:-}" = 1 ]; then echo probe-warning >&2; fi
 exit "${PROBE_EXIT:-0}"
fi
echo executed >> "$0.work"
printf '%s\n' "$PWD"
`
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin+".version", []byte(version), 0600); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestRuntimeVersionGateBeforeEveryLaunch(t *testing.T) {
	runtimePool(t)
	for _, version := range []string{"0.32.9", "0.33.0", "0.33.1-rc.1", "unknown"} {
		for _, method := range []string{"Start", "Output", "CombinedOutput"} {
			t.Run(version+"/"+method, func(t *testing.T) {
				bin := versionFixture(t, version)
				c := Command(t.Context(), bin, "", "run", "model.qnt")
				var err error
				switch method {
				case "Start":
					err = c.Start()
				case "Output":
					_, err = c.Output()
				case "CombinedOutput":
					_, err = c.CombinedOutput()
				}
				if err == nil || !strings.Contains(err.Error(), "unsupported Quint version") || c.Process != nil {
					t.Fatalf("unsupported workload launched: %v process=%v", err, c.Process)
				}
				if _, err := os.Stat(bin + ".work"); !os.IsNotExist(err) {
					t.Fatalf("solver executed: %v", err)
				}
			})
		}
	}
	bin := versionFixture(t, "0.33.0")
	v, err := Version(t.Context(), bin, "")
	if err != nil || v != "0.33.0" {
		t.Fatalf("introspection facts lost: %q %v", v, err)
	}
}

func TestVersionProbeUsesConfiguredEnvironmentAndDirectory(t *testing.T) {
	runtimePool(t)
	bin := versionFixture(t, "0.33.1")
	first, second := t.TempDir(), t.TempDir()
	for dir, v := range map[string]string{first: "0.33.1", second: "0.33.0"} {
		if err := os.WriteFile(filepath.Join(dir, "version"), []byte(v), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := Command(t.Context(), bin, first, "run")
	c.Env = append(os.Environ(), "PROBE_FROM_CWD=1", "PROBE_WARNING=1")
	out, err := c.Output()
	if err != nil || strings.TrimSpace(string(out)) != first {
		t.Fatalf("configured cwd/output: %q %v", out, err)
	}
	c = Command(t.Context(), bin, second, "run")
	c.Env = append(os.Environ(), "PROBE_FROM_CWD=1")
	if err := c.Run(); err == nil || c.Process != nil {
		t.Fatalf("cwd-selected old CLI admitted: %v", err)
	}
	c = Command(t.Context(), bin, first, "run")
	c.Env = append(os.Environ(), "PROBE_VERSION=0.33.0")
	if err := c.Run(); err == nil || c.Process != nil {
		t.Fatalf("Env-selected old CLI admitted: %v", err)
	}
	// A delegated file can change without replacing the wrapper. Never memoize it.
	if err := os.WriteFile(bin+".version", []byte("0.33.0"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Command(t.Context(), bin, first, "run").Run(); err == nil {
		t.Fatal("wrapper delegate change reused an earlier admission")
	}
}

func TestVersionProbeFailureCancellationAndRetry(t *testing.T) {
	runtimePool(t)
	bin := versionFixture(t, "0.33.1")
	c := Command(t.Context(), bin, "", "run")
	c.Env = append(os.Environ(), "PROBE_EXIT=7")
	if err := c.Run(); err == nil || c.Process != nil {
		t.Fatalf("failed version probe admitted workload: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	c = Command(ctx, bin, "", "run")
	c.Env = append(os.Environ(), "PROBE_HOLD=1")
	done := make(chan error, 1)
	go func() { done <- c.Run() }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(bin + ".started"); err == nil {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("probe did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || c.Process != nil {
			t.Fatalf("probe cancellation: %v process=%v", err, c.Process)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not cancel")
	}
	if err := Command(t.Context(), bin, "", "run").Run(); err != nil {
		t.Fatalf("cancelled/failed probe poisoned retry or leaked permit: %v", err)
	}
}

func stockQuint(t *testing.T) string {
	t.Helper()
	// This control selects the clean stock execution profile; override dispatch
	// is exercised separately and deliberately never memoized.
	for _, name := range []string{"NODE_OPTIONS", "NODE_PATH", "LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH"} {
		t.Setenv(name, "")
	}
	path, err := exec.LookPath("quint")
	if err != nil {
		skip.Optional(t, "native Quint unavailable: install the admitted Quint CLI")
	}
	c := Command(t.Context(), path, t.TempDir(), "run")
	if stockIdentity(c.raw()) == "" {
		skip.Optional(t, "stock memo control requires the admitted npm CLI and native Node without runtime overrides")
	}
	return path
}

func TestStockAdmissionMemoAcrossModelDirectories(t *testing.T) {
	runtimePool(t)
	bin := stockQuint(t)
	for range 2 {
		if err := Require(t.Context(), bin, t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := Command(t.Context(), bin, t.TempDir(), "--help").CombinedOutput(); err != nil {
		t.Fatalf("native workload control: %v %s", err, out)
	}
	// With both slots occupied, another model directory can succeed only if the
	// reviewed tool identity is reused without spawning a third managed process.
	first, err := acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	second, err := acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer second()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := Require(ctx, bin, t.TempDir()); err != nil {
		t.Fatalf("fresh model directory missed admitted stock identity: %v", err)
	}
}

func TestScriptNodeIsNotMemoizedAcrossDirectories(t *testing.T) {
	runtimePool(t)
	bin := stockQuint(t)
	fakeBin := t.TempDir()
	node := filepath.Join(fakeBin, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\nif [ \"$2\" = --version ]; then cat version; else echo work; fi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, v := range []string{"0.33.1", "0.33.0"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "version"), []byte(v), 0600); err != nil {
			t.Fatal(err)
		}
		c := Command(t.Context(), bin, dir, "--help")
		c.Env = env
		out, err := c.Output()
		if v == "0.33.1" {
			if err != nil || strings.TrimSpace(string(out)) != "work" {
				t.Fatalf("script interpreter positive: %q %v", out, err)
			}
		} else if err == nil || c.Process != nil {
			t.Fatalf("script interpreter's changed delegate bypassed admission: %q %v", out, err)
		}
	}
}

func TestNativeNodeProxyIsNotMemoizedAcrossDirectories(t *testing.T) {
	runtimePool(t)
	bin := stockQuint(t)
	fakeBin := t.TempDir()
	node := filepath.Join(fakeBin, "node")
	src := filepath.Join(fakeBin, "node.go")
	program := `package main
import("fmt";"os")
func main(){if len(os.Args)>2 && os.Args[2]=="--version" { b,e:=os.ReadFile("version");if e!=nil{panic(e)};fmt.Print(string(b)) } else {fmt.Println("work")}}
`
	if err := os.WriteFile(src, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", node, src).CombinedOutput(); err != nil {
		t.Fatalf("build proxy: %v %s", err, out)
	}
	env := append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, v := range []string{"0.33.1", "0.33.1", "0.33.0"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "version"), []byte(v), 0600); err != nil {
			t.Fatal(err)
		}
		c := Command(t.Context(), bin, dir, "--help")
		c.Env = env
		out, err := c.Output()
		if v == "0.33.1" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil || c.Process != nil {
			t.Fatalf("native proxy delegate bypassed admission: %q %v", out, err)
		}
	}
}

func TestNodePreloadIsNotMemoized(t *testing.T) {
	runtimePool(t)
	bin := stockQuint(t)
	hook := filepath.Join(t.TempDir(), "version-hook.js")
	env := append(os.Environ(), "NODE_OPTIONS=--require "+hook)
	for _, version := range []string{"0.33.1", "0.33.0"} {
		source := `const Module = require('module');
const load = Module._load;
Module._load = function(id, parent, main) {
 if (id === './version' && parent && parent.filename.endsWith('/dist/src/cli.js')) return {version: 'VERSION'};
 return load.apply(this, arguments);
};`
		if err := os.WriteFile(hook, []byte(strings.ReplaceAll(source, "VERSION", version)), 0600); err != nil {
			t.Fatal(err)
		}
		c := Command(t.Context(), bin, t.TempDir(), "--help")
		c.Env = env
		if err := c.Run(); version == "0.33.1" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil || c.Process != nil {
			t.Fatalf("changed preload reused stock admission: %v", err)
		}
	}
}
