package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

func TestReportDeliveryFailuresRetainEvidence(t *testing.T) {
	for _, initial := range []string{"pass", "model failure", "tool failure"} {
		t.Run(initial, func(t *testing.T) {
			dir := t.TempDir()
			envDir := filepath.Join(dir, "env")
			t.Setenv("GOTOGETHER_REPORT_DIR", envDir)
			good := filepath.Join(dir, "good.json")
			later := filepath.Join(dir, "later.json")
			m := machineReports{json: true, files: repeated{"json=" + good, "json=" + dir, "json=" + later, "json=" + filepath.Join(dir, "missing", "out.json")}}
			d := validate.NewDocument("simulate")
			d.Verdict = "pass"
			d.Checks = []validate.DocCheck{{Name: "safe", Kind: "invariant", Verdict: "pass"}}
			var original error
			code := 0
			if initial == "model failure" {
				code = 1
				d.Verdict = "fail"
				d.Reason = "violates safe"
				d.Checks[0].Verdict = "fail"
			}
			wantCheck, wantReason := d.Checks[0].Verdict, d.Reason
			if initial == "tool failure" {
				original = errors.New("native tool failed")
				code = 2
			}
			var out bytes.Buffer
			got, err := m.write(d, &out, code, original)
			if got != 2 || err == nil || (original != nil && !errors.Is(err, original)) {
				t.Fatalf("status %d, %v", got, err)
			}
			for _, want := range []string{"json=" + dir, "missing"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("missing delivery failure %q: %v", want, err)
				}
			}
			paths := []string{good, later}
			entries, e := os.ReadDir(envDir)
			if e != nil || len(entries) != 1 {
				t.Fatalf("environment reports %v %v", entries, e)
			}
			paths = append(paths, filepath.Join(envDir, entries[0].Name()))
			docs := [][]byte{out.Bytes()}
			for _, p := range paths {
				b, e := os.ReadFile(p)
				if e != nil {
					t.Fatal(e)
				}
				docs = append(docs, b)
			}
			for _, b := range docs {
				var r validate.Document
				if e := json.Unmarshal(b, &r); e != nil {
					t.Fatal(e)
				}
				if r.Exit != 2 || r.Verdict != "error" || r.Error != err.Error() || len(r.Checks) != 1 || r.Checks[0].Verdict != wantCheck || r.Reason != wantReason {
					t.Fatalf("lost command status or native evidence: %s", b)
				}
			}
		})
	}
}

type failingReportWriter struct{ err error }

func (w failingReportWriter) Write([]byte) (int, error) { return 0, w.err }

func TestReportStdoutFailureRefreshesSavedReport(t *testing.T) {
	t.Setenv("GOTOGETHER_REPORT_DIR", "")
	p := filepath.Join(t.TempDir(), "report.json")
	m := machineReports{json: true, files: repeated{"json=" + p}}
	cause := errors.New("broken pipe")
	code, err := m.write(validate.NewDocument("simulate"), failingReportWriter{cause}, 0, nil)
	if code != 2 || !errors.Is(err, cause) {
		t.Fatalf("%d %v", code, err)
	}
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	var d validate.Document
	if e = json.Unmarshal(b, &d); e != nil {
		t.Fatal(e)
	}
	if d.Exit != 2 || d.Verdict != "error" || !strings.Contains(d.Error, "stdout") {
		t.Fatalf("stale saved command status: %s", b)
	}
}

func TestReportNativeSimulationDeliveryFailure(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	t.Setenv("GOTOGETHER_REPORT_DIR", "")
	good := filepath.Join(dir, "result.json")
	junit := filepath.Join(dir, "result.xml")
	sarif := filepath.Join(dir, "result.sarif")
	code, out := capture(t, func() (int, error) {
		code, err := simulate([]string{"-json", "-graph", "-binding", "../../validate/testdata/vending.yaml", "-report", "json=" + good, "-report", "junit=" + junit, "-report", "sarif=" + sarif, "-report", "json=" + dir})
		if err == nil {
			t.Error("missing report delivery error")
		}
		return code, nil
	})
	var d validate.Document
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if code != 2 || d.Exit != 2 || d.Verdict != "error" || d.Graph == nil || !d.Graph.Complete || len(d.Checks) == 0 {
		t.Fatalf("native evidence or command error lost: %s", out)
	}
	b, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, []byte(out)) {
		t.Fatalf("saved JSON differs from final stdout: %s", b)
	}
	for _, p := range []string{junit, sarif} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(b, []byte("-report json=")) {
			t.Fatalf("missing delivery failure in %s: %s", p, b)
		}
	}
}

func TestReportEnvironmentDeliveryFailure(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "file")
	if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOTOGETHER_REPORT_DIR", blocked)
	good := filepath.Join(dir, "report.json")
	var out bytes.Buffer
	m := machineReports{json: true, files: repeated{"json=" + good}}
	code, err := m.write(validate.NewDocument("validate"), &out, 0, nil)
	if code != 2 || err == nil || !strings.Contains(err.Error(), "GOTOGETHER_REPORT_DIR") {
		t.Fatalf("%d %v", code, err)
	}
	b, e := os.ReadFile(good)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(b, out.Bytes()) {
		t.Fatalf("saved report differs from final status: %s", b)
	}
}
