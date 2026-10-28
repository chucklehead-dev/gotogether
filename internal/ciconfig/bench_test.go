package ciconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// compareCSV is benchstat's CSV of two runs (ci/bench.sh run): a package
// whose time regressed past the threshold and one whose allocations did,
// changes that are improvements or not significant, and a section of
// benchmarks the base lacked.
const compareCSV = `goos: linux
goarch: amd64
pkg: github.com/chucklehead-dev/gotogether/qobs
cpu: Intel(R) Xeon(R) Processor @ 2.10GHz
,old,,new,,,
,sec/op,CI,sec/op,CI,vs base,P
Record/PerStep-4,1.8e-05,3%,3.0e-06,2%,-83.33%,p=0.002 n=6
Record/Buffered-4,1.0e-05,3%,1.4e-05,2%,+40.00%,p=0.002 n=6
Record/Thread-4,1.0e-05,3%,1.2e-05,2%,+20.00%,p=0.002 n=6
Record/Global-4,1.0e-05,3%,1.9e-05,40%,~,p=0.310 n=6
geomean,1.0e-05,,1.0e-05,,+5.00%,

,old,,new,,,
,allocs/op,CI,allocs/op,CI,vs base,P
Record/PerStep-4,48,0%,8,0%,-83.33%,p=0.002 n=6
Record/Buffered-4,8,0%,9,0%,+12.50%,p=0.002 n=6
geomean,10,,10,,+0.00%,

pkg: github.com/chucklehead-dev/gotogether/qtrace
,new,
,sec/op,CI
MarshalLine-4,3.0e-06,2%
geomean,3.0e-06,
`

// ci/bench.sh check fails on a significant regression past its
// threshold, and on no other change: time past 25%, allocations past
// 10%; an improvement, a change within the threshold or one benchstat
// finds not significant passes.
func TestBenchCheck(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	csv := filepath.Join(t.TempDir(), "compare.csv")
	if err := os.WriteFile(csv, []byte(compareCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", "../../ci/bench.sh", "check", csv).CombinedOutput()
	if err == nil {
		t.Fatalf("no regression found:\n%s", out)
	}
	got := strings.TrimSpace(string(out))
	want := []string{
		"::error::github.com/chucklehead-dev/gotogether/qobs Record/Buffered-4: sec/op +40.00% (p=0.002 n=6): past the 25% threshold",
		"::error::github.com/chucklehead-dev/gotogether/qobs Record/Buffered-4: allocs/op +12.50% (p=0.002 n=6): past the 10% threshold",
	}
	if got != strings.Join(want, "\n") {
		t.Fatalf("ci/bench.sh check said:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
	// the thresholds are the script's knobs
	cmd := exec.Command("bash", "../../ci/bench.sh", "check", csv)
	cmd.Env = append(os.Environ(), "SEC=50", "MEM=20")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("past raised thresholds: %v\n%s", err, out)
	}
}

// The benchmarks run nightly, compared by ci/bench.sh with the commit
// main was at a day before, and never on a push: the per-push CI stays
// as fast as it was.
func TestBenchmarksNightlyOnly(t *testing.T) {
	nightly, err := os.ReadFile("../../.github/workflows/nightly.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ci/bench.sh run", "ci/bench.sh check", "golang.org/x/perf/cmd/benchstat@"} {
		if !strings.Contains(string(nightly), want) {
			t.Errorf("nightly.yml does not run %q", want)
		}
	}
	ci, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(ci), "\n") {
		if strings.Contains(line, "ci/bench.sh") || strings.Contains(line, " -bench ") || strings.Contains(line, " -bench=") {
			t.Errorf("ci.yml runs benchmarks on every push: %s", strings.TrimSpace(line))
		}
	}
}
