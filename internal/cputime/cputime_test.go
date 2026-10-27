package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// cpuLine runs cmd under run and returns the CPU and wall seconds of the
// line it appended.
func cpuLine(t *testing.T, script string) (cpu, wall float64) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "cpu.txt")
	if code := run("sh", []string{"-c", script}, out); code != 0 {
		t.Fatalf("run exited %d", code)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(b))
	if len(f) != 3 {
		t.Fatalf("line %q: want <dir> <cpu> <wall>", b)
	}
	if wd, _ := os.Getwd(); f[0] != wd {
		t.Errorf("dir %s, want the working directory %s (go test runs a binary in its package's)", f[0], wd)
	}
	cpu, _ = strconv.ParseFloat(f[1], 64)
	wall, _ = strconv.ParseFloat(f[2], 64)
	return cpu, wall
}

// A busy loop's time is counted in a grandchild waited for by its parent
// (quint's evaluator under node, under the test binary), and in an
// orphan its parent left behind that exits before the binary does.
func TestCountsDescendants(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("orphans are counted on Linux only (the subreaper)")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	const busy = `i=0; while [ $i -lt 400000 ]; do i=$((i+1)); done`
	cpu, _ := cpuLine(t, `sh -c '`+busy+`'`)
	if cpu < 0.1 {
		t.Errorf("grandchild's busy loop: %.1f CPU seconds counted, want it counted (at least 0.1)", cpu)
	}
	cpu, wall := cpuLine(t, `(sh -c '`+busy+`' &); sleep 3`)
	if cpu < 0.1 {
		t.Errorf("orphan's busy loop: %.1f CPU seconds counted, want it counted (at least 0.1)", cpu)
	}
	if wall < 2.5 {
		t.Errorf("wall %.1f s, want the binary's whole run (3 s)", wall)
	}
}

func TestExitCode(t *testing.T) {
	if code := run("sh", []string{"-c", "exit 3"}, ""); code != 3 {
		t.Errorf("exit code %d, want the binary's 3 (go test reads it)", code)
	}
	if code := run("sh", []string{"-c", "kill -TERM $$"}, ""); code != 128+15 {
		t.Errorf("exit code %d, want 143 for a binary killed by SIGTERM", code)
	}
}

// budget runs ci/time-budget.sh on lines with budgets from times.
func budget(t *testing.T, times, lines string) (int, string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tf := filepath.Join(t.TempDir(), "test-times.txt")
	if err := os.WriteFile(tf, []byte(times), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(root, "ci", "time-budget.sh"))
	cmd.Env = append(os.Environ(), "TIMES="+tf)
	cmd.Stdin = strings.NewReader(strings.ReplaceAll(lines, "ROOT", root))
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	} else if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

func TestTimeBudget(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	const times = "# comment\nvalidate 100 # a reason\nexamples/bank 10\n"
	for _, c := range []struct {
		name, lines string
		code        int
		want        string
	}{
		{"under", "ROOT/validate 199.0 50.0\nROOT/examples/bank 20.0 3.0\n", 0, ""},
		{"over", "ROOT/validate 201.5 50.0\n", 1, "validate's tests took 201.5 CPU seconds (50.0s wall), more than twice its 100"},
		{"over, with the fix", "ROOT/examples/bank 20.1 3.0\n", 1, "set its line to the new CPU time with the reason"},
		// a package without a line has no budget: only packages over ten
		// seconds have one
		{"missing line", "ROOT/qobs 500.0 9.0\nROOT/validate 10.0 5.0\n", 0, ""},
		{"go test's output", "ok  \tgithub.com/chucklehead-dev/gotogether/validate\t530.1s\n", 2, "not go test's output"},
		{"nothing read", "", 2, "read no CPU times"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, out := budget(t, times, c.lines)
			if code != c.code {
				t.Errorf("exit %d, want %d\n%s", code, c.code, out)
			}
			if c.want != "" && !strings.Contains(out, c.want) {
				t.Errorf("output %q, want it to say %q", out, c.want)
			}
			if c.code == 0 && out != "" {
				t.Errorf("unexpected output %q", out)
			}
		})
	}
}
