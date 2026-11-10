package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

func skippedSimulationBinding(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "tiny.qnt"), []byte("module tiny { var x: int action init = x' = 0 action step = x' = x }"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("spec: tiny.qnt\nmodule: tiny\nsimulate:\n  skip: %q\n", name)), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func simulationDocuments(t *testing.T, dir string) map[string]validate.Document {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	docs := map[string]validate.Document{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var doc validate.Document
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		if _, duplicate := docs[doc.Binding]; duplicate {
			t.Fatalf("multiple documents for %s", doc.Binding)
		}
		docs[doc.Binding] = doc
	}
	return docs
}

func TestSimulateManyIndependentReports(t *testing.T) {
	dir := t.TempDir()
	a := skippedSimulationBinding(t, dir, "first")
	b := skippedSimulationBinding(t, dir, "second")
	reports := filepath.Join(dir, "reports")
	decoy := filepath.Join(dir, "environment-reports")
	t.Setenv("GOTOGETHER_REPORT_DIR", decoy)
	var code int
	var runErr error
	text := captureStdout(t, func() {
		code, runErr = simulate(t.Context(), []string{"-binding", a, "-binding", b, "-verify", "2", "-quint", "/nonexistent/quint", "-report-dir", reports})
	})
	if code != 0 || runErr != nil {
		t.Fatalf("exit %d, %v", code, runErr)
	}
	if !strings.Contains(text, a) || strings.Index(text, b) <= strings.Index(text, a) {
		t.Fatalf("binding order lost: %s", text)
	}
	docs := simulationDocuments(t, reports)
	if len(docs) != 2 || docs[a].Binding != a || docs[a].SkipReason != "first" || docs[b].SkipReason != "second" || docs[a].Exit != 0 || docs[b].Exit != 0 {
		t.Fatalf("independent skips lost: %+v", docs)
	}
	if _, err := os.Stat(decoy); !os.IsNotExist(err) || os.Getenv("GOTOGETHER_REPORT_DIR") != decoy {
		t.Fatalf("explicit report directory changed environment: %v", err)
	}
}

// One binding's error does not stop the roster: every binding after it
// still reports, and the run exits 2.
func TestSimulateManyContinuesPastAnAttemptedBindingError(t *testing.T) {
	dir := t.TempDir()
	a := skippedSimulationBinding(t, dir, "first")
	b := filepath.Join(dir, "missing.yaml")
	c := skippedSimulationBinding(t, dir, "visited")
	reports := filepath.Join(dir, "reports")
	t.Setenv("GOTOGETHER_REPORT_DIR", "")
	var code int
	var runErr error
	text := captureStdout(t, func() {
		code, runErr = simulate(t.Context(), []string{"-binding", a, "-binding", b, "-binding", c, "-report-dir", reports})
	})
	if code != 2 || runErr != nil || !strings.Contains(text, "visited") {
		t.Fatalf("stopped at the missing binding: %d %v %s", code, runErr, text)
	}
	docs := simulationDocuments(t, reports)
	if len(docs) != 3 || docs[a].SkipReason != "first" || docs[c].SkipReason != "visited" || docs[b].Binding != b || docs[b].Verdict != "error" || docs[b].Error == "" || len(docs[b].Checks) != 0 || docs[b].SkipReason != "" {
		t.Fatalf("error reused another binding's result: %+v", docs)
	}
}

// A slow proof first must not leave the bindings after it unvisited: with
// -timeout every binding reports, the slow one unfinished (no verdict,
// never a pass or a skip), and the run is red. The deadline leaves each fast
// binding a 15 s first share: they still start real quint processes, which a
// loaded runner can hold past a second or two, while the slow one never ends.
func TestSimulateManyTimeoutReportsEveryBinding(t *testing.T) {
	skip.Quint(t)
	t.Setenv("GOTOGETHER_REPORT_DIR", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tiny.qnt"), []byte("module tiny { var x: int action init = x' = 0 action step = x' = x val slow = x >= 0 val fast = x == 0 }"), 0600); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, name := range []string{"slow", "fast", "fast2"} {
		inv := strings.TrimSuffix(name, "2")
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte("spec: tiny.qnt\nmodule: tiny\ninvariants: ["+inv+"]\nsimulate:\n  holds: ["+inv+"]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	real, err := exec.LookPath("quint")
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
	script := "#!/bin/sh\nset -eu\nif [ \"$1\" != verify ]; then exec " + quote(real) + " \"$@\"; fi\n" +
		"case \" $* \" in *\" --invariant slow \"*) sleep 600;; esac\n" +
		"out=''\nwhile [ $# -gt 0 ]; do if [ \"$1\" = --out ]; then out=$2; shift; fi; shift; done\n" +
		"printf '%s\\n' '{\"stage\":\"verifying\",\"status\":\"ok\",\"errors\":[]}' > \"$out\"\n"
	fake := filepath.Join(dir, "quint-custom")
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	reports := filepath.Join(dir, "reports")
	var code int
	var runErr error
	start := time.Now()
	captureStdout(t, func() {
		code, runErr = simulate(t.Context(), []string{"-binding", paths[0], "-binding", paths[1], "-binding", paths[2], "-verify", "2", "-quint", fake, "-report-dir", reports, "-timeout", "45s"})
	})
	if elapsed := time.Since(start); elapsed > 90*time.Second {
		t.Fatalf("the run outlived -timeout: %s", elapsed)
	}
	if code != 2 || runErr != nil {
		t.Fatalf("an unfinished binding must leave the run red: %d %v", code, runErr)
	}
	docs := simulationDocuments(t, reports)
	if len(docs) != 3 {
		t.Fatalf("every binding must report: %+v", docs)
	}
	for _, fast := range paths[1:] {
		if d := docs[fast]; d.Verdict != "pass" || d.Unfinished != "" || len(d.Checks) != 1 || d.Checks[0].Verdict != "holds" {
			t.Fatalf("a binding after the slow one did not pass: %+v", d)
		}
	}
	if d := docs[paths[0]]; d.Verdict != "no verdict" || d.Exit != 2 || d.Unfinished == "" || d.SkipReason != "" || len(d.Checks) != 0 {
		t.Fatalf("the slow binding is not reported unfinished: %+v", d)
	}
}

func TestSimulateManyPreflight(t *testing.T) {
	dir := t.TempDir()
	a := skippedSimulationBinding(t, dir, "first")
	b := skippedSimulationBinding(t, dir, "second")
	alias := filepath.Join(dir, "alias.yaml")
	if err := os.Symlink(a, alias); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-binding", a, "-binding", alias}, "duplicate binding"},
		{[]string{"-binding", a, "-binding", b, "-json"}, "independent reports"},
		{[]string{"-binding", a, "-binding", b, "-report", "json=" + filepath.Join(dir, "combined.json")}, "independent reports"},
		{[]string{"-binding", a, "-binding", b, "-graph", "-out", filepath.Join(dir, "combined.qnt")}, "one graph artifact"},
	} {
		var reports machineReports
		if _, err := simulationRequest(tc.args, &reports, &simLog{}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: want %q, got %v", tc.args, tc.want, err)
		}
	}
}

func TestSimulateManyReportDeliveryStopsIteration(t *testing.T) {
	skip.Quint(t)
	t.Setenv("GOTOGETHER_REPORT_DIR", "")
	dir := t.TempDir()
	model := filepath.Join(dir, "tiny.qnt")
	if err := os.WriteFile(model, []byte("module tiny { var x: int action init = x' = 0 action step = x' = x val good = x >= 0 val alsoGood = x == 0 }"), 0600); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, name := range []string{"first", "unvisited"} {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte("spec: tiny.qnt\nmodule: tiny\ninvariants: [good, alsoGood]\nsimulate:\n  holds: [good, alsoGood]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	real, err := exec.LookPath("quint")
	if err != nil {
		t.Fatal(err)
	}
	reports, calls := filepath.Join(dir, "reports"), filepath.Join(dir, "calls")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
	script := "#!/bin/sh\nset -eu\nif [ \"$1\" != verify ]; then exec " + quote(real) + " \"$@\"; fi\n" +
		"printf 'verify\\n' >> " + quote(calls) + "\n" +
		"rmdir " + quote(reports) + "; printf blocked > " + quote(reports) + "\n" +
		"out=''\nwhile [ $# -gt 0 ]; do if [ \"$1\" = --out ]; then out=$2; shift; fi; shift; done\n" +
		"printf '%s\\n' '{\"stage\":\"verifying\",\"status\":\"ok\",\"errors\":[]}' > \"$out\"\n"
	fake := filepath.Join(dir, "quint-custom")
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var code int
	var runErr error
	captureStdout(t, func() {
		code, runErr = simulate(t.Context(), []string{"-binding", paths[0], "-binding", paths[1], "-verify", "2", "-quint", fake, "-report-dir", reports})
	})
	if code != 2 || runErr == nil || !strings.Contains(runErr.Error(), "-report-dir") {
		t.Fatalf("delivery failure lost: %d %v", code, runErr)
	}
	data, err := os.ReadFile(calls)
	if err != nil || string(data) != "verify\n" {
		t.Fatalf("analysis continued after report delivery failed: %q %v", data, err)
	}
}

// The public CLI must exercise the real managed backend and its own proof
// session, without a backend wrapper that would deliberately disable reuse.
func TestSimulateManyNativeProofReuse(t *testing.T) {
	if os.Getenv("GOTOGETHER_NATIVE_VERIFY") != "1" {
		t.Skip("set GOTOGETHER_NATIVE_VERIFY=1 for the tiny managed native CLI control")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tiny.qnt"), []byte(`module tiny {
 var x: int
 action init = x' = 0
 action step = x' = if (x < 1) x + 1 else x
 val nonnegative = x >= 0
 val bounded = x <= 1
}`), 0600); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for i, name := range []string{"atZero", "atOne"} {
		path := filepath.Join(dir, name+".yaml")
		body := fmt.Sprintf("spec: tiny.qnt\nmodule: tiny\ninvariants: [nonnegative, bounded]\nsimulate:\n  holds: [nonnegative, bounded]\n  possible:\n    - name: %s\n      when: x == %d\n", name, i)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	reports := filepath.Join(dir, "reports")
	var code int
	var runErr error
	text := captureStdout(t, func() {
		code, runErr = simulate(t.Context(), []string{"-binding", paths[0], "-binding", paths[1], "-verify", "2", "-report-dir", reports})
	})
	if code != 0 || runErr != nil {
		t.Fatalf("managed CLI: %d %v\n%s", code, runErr, text)
	}
	docs := simulationDocuments(t, reports)
	for i, path := range paths {
		doc := docs[path]
		if doc.Verdict != "pass" || len(doc.Checks) != 3 {
			t.Fatalf("lost this binding's independent checks: %+v", doc)
		}
		for _, check := range doc.Checks[:2] {
			proof := check.NativeProof
			if check.Verdict != "holds" || check.Bound == nil || check.Bound.Steps != 2 || !check.Bound.Proves || proof == nil || proof.ProducerBinding != paths[0] || proof.Reused != (i == 1) || len(proof.RequestSHA256) != 64 {
				t.Fatalf("wrong proof origin/scope for %s: %+v", path, check)
			}
			if proof.RequestSHA256 != docs[paths[0]].Checks[0].NativeProof.RequestSHA256 {
				t.Fatal("exact request identity changed between reports")
			}
		}
		if goal := doc.Checks[2]; goal.Name != strings.TrimSuffix(filepath.Base(path), ".yaml") || goal.Verdict != "reached" {
			t.Fatalf("this binding's possible was skipped/replaced: %+v", goal)
		}
	}
	if !strings.Contains(text, paths[0]) || !strings.Contains(text, paths[1]) {
		t.Fatalf("text lost binding identity: %s", text)
	}
	if evidence := os.Getenv("GOTOGETHER_NATIVE_REUSE_RECEIPT"); evidence != "" {
		if err := os.MkdirAll(evidence, 0700); err != nil {
			t.Fatal(err)
		}
		for i, path := range paths {
			file, err := os.OpenFile(filepath.Join(evidence, fmt.Sprintf("%d-report.json", i)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				t.Fatal(err)
			}
			doc := docs[path]
			_, writeErr := file.Write(doc.JSON())
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				t.Fatalf("retain native CLI evidence: %v %v", writeErr, closeErr)
			}
		}
	}
}
