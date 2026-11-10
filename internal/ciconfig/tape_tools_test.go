package ciconfig

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
)

type tapeToolFixture struct {
	bin, root string
	env       []string
}

func newTapeToolFixture(t *testing.T, present ...string) *tapeToolFixture {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("tape bootstrap uses Linux apt and GNU timeout")
	}
	f := &tapeToolFixture{bin: t.TempDir(), root: t.TempDir()}
	for _, command := range []string{"bash", "timeout", "env", "uname", "mkdir", "cp", "chmod", "sleep"} {
		path, err := exec.LookPath(command)
		if err != nil {
			t.Fatalf("Linux tape bootstrap prerequisite %s: %v", command, err)
		}
		if err := os.Symlink(path, filepath.Join(f.bin, command)); err != nil {
			t.Fatal(err)
		}
	}
	f.env = append(os.Environ(), "PATH="+f.bin, "TAPE_FIXTURE_BIN="+f.bin,
		"TAPE_FIXTURE_ROOT="+f.root, "GITHUB_PATH="+filepath.Join(f.root, "github-path"),
		"TAPE_TOOLS_TIMEOUT_SECONDS=5")
	tools := map[string]string{
		"ttyd-template": `#!/bin/sh
printf 'ttyd version %s\n' "${TAPE_FIXTURE_TTYD_VERSION:-1.7.7}"
`,
		"ffmpeg-template": `#!/bin/sh
[ "${TAPE_FIXTURE_FFMPEG:-ok}" = broken ] && exit 44
printf 'ffmpeg version 8.0.1\n'
`,
		"vhs-template": `#!/bin/sh
[ "${TAPE_FIXTURE_VHS:-ok}" = broken ] && exit 45
printf 'vhs version development\n'
`,
		"sudo": `#!/bin/sh
[ "$1" = -n ] || exit 90
shift
exec "$@"
`,
		"apt-get": `#!/bin/sh
printf '%s\n' "$*" >> "$TAPE_FIXTURE_ROOT/apt-calls"
case "${TAPE_FIXTURE_APT:-ok}" in
  fail) exit 42 ;;
  slow) sleep 2; printf 'slow command completed\n' >> "$TAPE_FIXTURE_ROOT/slow-completed" ;;
esac
case "$*" in
  *install*)
    if [ "${TAPE_FIXTURE_APT:-ok}" != absent ]; then
      for tool in "$@"; do
        case "$tool" in
          ttyd|ffmpeg)
            if [ "${TAPE_FIXTURE_APT:-ok}:$tool" != no-ffmpeg:ffmpeg ]; then
              cp "$TAPE_FIXTURE_BIN/$tool-template" "$TAPE_FIXTURE_BIN/$tool"
            fi
            ;;
        esac
      done
    fi
    ;;
esac
`,
		"go": `#!/bin/sh
printf '%s\n' "$*" >> "$TAPE_FIXTURE_ROOT/go-calls"
case "$1 $2" in
  'env GOPATH') printf '%s\n' "$TAPE_FIXTURE_ROOT/gopath" ;;
  'install github.com/charmbracelet/vhs@v0.12.1')
    if [ "${TAPE_FIXTURE_GO:-ok}" = fail ]; then exit 43; fi
    if [ "${TAPE_FIXTURE_GO:-ok}" != absent ]; then
      cp "$TAPE_FIXTURE_BIN/vhs-template" "$GOBIN/vhs"
    fi
    ;;
  'version -m')
    printf '\tpath\tgithub.com/charmbracelet/vhs\n\tmod\tgithub.com/charmbracelet/vhs\t%s\th1:fixture\n' "${TAPE_FIXTURE_VHS_VERSION:-v0.12.1}"
    ;;
  *) exit 91 ;;
esac
`,
	}
	for name, source := range tools {
		if err := os.WriteFile(filepath.Join(f.bin, name), []byte(source), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range present {
		source, err := os.ReadFile(filepath.Join(f.bin, name+"-template"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.bin, name), source, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *tapeToolFixture) run(t *testing.T, script string, extra ...string) (string, error) {
	t.Helper()
	// Also bounds a broken negative-control script that loses its timeout.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(f.bin, "bash"), script)
	command.Env = append(append([]string{}, f.env...), extra...)
	out, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("bootstrap escaped its command bound: %v\n%s", ctx.Err(), out)
	}
	return string(out), err
}

func (f *tapeToolFixture) calls(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root, name+"-calls"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func tapeToolScript(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs("../../ci/install-tape-tools.sh")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTapeToolsReuseAndAcquire(t *testing.T) {
	for _, tc := range []struct {
		name    string
		present []string
		wantAPT string
	}{
		{"warm", []string{"ttyd", "ffmpeg", "vhs"}, ""},
		{"one missing", []string{"ffmpeg"}, "install -y --no-install-recommends ttyd\n"},
		{"cold", nil, "install -y --no-install-recommends ttyd ffmpeg\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newTapeToolFixture(t, tc.present...)
			output, err := fixture.run(t, tapeToolScript(t))
			if err != nil {
				t.Fatalf("bootstrap: %v\n%s", err, output)
			}
			apt := fixture.calls(t, "apt")
			if tc.wantAPT == "" {
				if apt != "" || strings.Contains(fixture.calls(t, "go"), "install ") {
					t.Fatalf("warm tools were acquired again: apt=%q go=%q", apt, fixture.calls(t, "go"))
				}
			} else {
				if strings.Count(apt, "\n") != 2 || !strings.HasSuffix(apt, tc.wantAPT) {
					t.Fatalf("must install only absent tools: %q", apt)
				}
				for _, bound := range []string{"DPkg::Lock::Timeout=30", "Acquire::http::Timeout=30", "Acquire::https::Timeout=30", "Acquire::Retries=2"} {
					if !strings.Contains(apt, bound) {
						t.Errorf("package acquisition omitted %s: %s", bound, apt)
					}
				}
				if !strings.Contains(fixture.calls(t, "go"), "install github.com/charmbracelet/vhs@v0.12.1\n") {
					t.Fatal("VHS was not acquired at the admitted pin")
				}
			}
			path, err := os.ReadFile(filepath.Join(fixture.root, "github-path"))
			wantPath := filepath.Join(fixture.root, "gopath", "bin")
			if tc.name == "warm" {
				wantPath = fixture.bin
			}
			if err != nil || string(path) != wantPath+"\n" {
				t.Fatalf("tool path not published: %q, %v", path, err)
			}
		})
	}
}

func TestTapeToolsAcquisitionFailuresCannotPass(t *testing.T) {
	for _, tc := range []struct {
		name, setting, want string
		present             []string
	}{
		{"apt failure", "TAPE_FIXTURE_APT=fail", "apt-get update failed (exit 42)", nil},
		{"apt missing output", "TAPE_FIXTURE_APT=absent", "ttyd is still missing", nil},
		{"apt missing ffmpeg", "TAPE_FIXTURE_APT=no-ffmpeg", "ffmpeg is still missing", nil},
		{"go failure", "TAPE_FIXTURE_GO=fail", "install VHS v0.12.1 failed (exit 43)", []string{"ttyd", "ffmpeg"}},
		{"go missing output", "TAPE_FIXTURE_GO=absent", "did not produce an executable vhs", []string{"ttyd", "ffmpeg"}},
		{"wrong artifact", "TAPE_FIXTURE_VHS_VERSION=v0.11.0", "does not identify module v0.12.1", []string{"ttyd", "ffmpeg"}},
		{"unsupported ttyd", "TAPE_FIXTURE_TTYD_VERSION=1.6.0", "ttyd must be at least 1.7", []string{"ttyd", "ffmpeg", "vhs"}},
		{"broken ffmpeg", "TAPE_FIXTURE_FFMPEG=broken", "check ffmpeg failed (exit 44)", []string{"ttyd", "ffmpeg", "vhs"}},
		{"broken VHS", "TAPE_FIXTURE_VHS=broken", "verify VHS failed (exit 45)", []string{"ttyd", "ffmpeg", "vhs"}},
		{"invalid deadline", "TAPE_TOOLS_TIMEOUT_SECONDS=0", "must be an integer from 1 to 600", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newTapeToolFixture(t, tc.present...)
			output, err := fixture.run(t, tapeToolScript(t), tc.setting)
			if err == nil || !strings.Contains(output, tc.want) || strings.Contains(output, "install-tape-tools: ready:") {
				t.Fatalf("failed acquisition passed or lost its diagnosis: %v\n%s", err, output)
			}
			if strings.HasPrefix(tc.name, "apt ") && strings.Contains(fixture.calls(t, "go"), "install ") {
				t.Fatal("apt failure continued into VHS acquisition")
			}
		})
	}
}

func TestTapeToolsSlowAcquisitionIsBounded(t *testing.T) {
	// Drive the total-budget clock from the owned command's completion marker.
	// Host scheduling before apt starts must not consume this test's budget:
	// it is the active command's real GNU timeout that this control exercises.
	// After unbounded apt finishes, the next phase observes an exhausted budget.
	source := readPreflightFile(t, tapeToolScript(t))
	clock := `left=$((deadline - SECONDS))`
	if strings.Count(source, clock) != 1 {
		t.Fatal("bootstrap remaining-budget clock changed")
	}
	source = strings.Replace(source, clock, `left=$budget
  if [[ -f "$TAPE_FIXTURE_ROOT/slow-completed" ]]; then left=0; fi`, 1)
	bounded := filepath.Join(t.TempDir(), "bounded.sh")
	if err := os.WriteFile(bounded, []byte(source), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := newTapeToolFixture(t)
	output, err := fixture.run(t, bounded, "TAPE_FIXTURE_APT=slow", "TAPE_TOOLS_TIMEOUT_SECONDS=1")
	exit, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exit.ExitCode() != 124 || !strings.Contains(output, "apt-get update failed (exit 124)") {
		t.Fatalf("slow package download escaped its deadline: %v\n%s", err, output)
	}
	if strings.Contains(fixture.calls(t, "go"), "install ") || strings.Contains(output, "install-tape-tools: ready:") {
		t.Fatal("timeout continued as successful bootstrap")
	}

	if _, err := os.Stat(filepath.Join(fixture.root, "slow-completed")); !os.IsNotExist(err) {
		t.Fatal("timed-out package command completed its delayed side effect")
	}
	// Removing only apt's timeout lets the same delayed side effect happen.
	// The remaining-budget check then fails, but cannot undo the late work:
	// this control proves the hard bound actually stops the active command.
	unbounded := strings.ReplaceAll(source,
		`sudo -n timeout --kill-after=10s "${left}s" env`, `sudo -n env`)
	if strings.Contains(unbounded, `sudo -n timeout`) {
		t.Fatal("negative control did not remove apt's timeout")
	}
	copy := filepath.Join(t.TempDir(), "unbounded.sh")
	if err := os.WriteFile(copy, []byte(unbounded), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture = newTapeToolFixture(t)
	output, err = fixture.run(t, copy, "TAPE_FIXTURE_APT=slow", "TAPE_TOOLS_TIMEOUT_SECONDS=1")
	if err == nil || !strings.Contains(output, "command budget exhausted") {
		t.Fatalf("unbounded negative control did not exhaust its budget: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(fixture.root, "slow-completed")); err != nil {
		t.Fatalf("negative control did not expose late package work: %v\n%s", err, output)
	}
}
