package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// validate -json prints the report as gotogether.report/1 and nothing
// else; -report writes its JUnit and SARIF renderings beside the text;
// GOTOGETHER_REPORT_DIR gets the JSON of every run (I20). A failing
// record is named by where it was read, with its wall time and process.
func TestValidateJSON(t *testing.T) {
	skip.Quint(t) // not parallel: Setenv
	dir := t.TempDir()
	log := filepath.Join(dir, "run.jsonl")
	f, err := os.Create(log)
	if err != nil {
		t.Fatal(err)
	}
	steps := []qtrace.Step{{Action: "withdraw", Actor: "over", Recorder: "over", Process: "over", Thread: "over", Seq: 1,
		Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(-5)}}}
	if err := qtrace.WriteJSONL(f, steps); err != nil {
		t.Fatal(err)
	}
	f.Close()
	reports := filepath.Join(dir, "reports")
	t.Setenv("GOTOGETHER_REPORT_DIR", reports)
	junit, sarif := filepath.Join(dir, "r.xml"), filepath.Join(dir, "r.sarif")
	code, out, err := validated([]string{"-json", "-report", "junit=" + junit, "-report", "sarif=" + sarif,
		"-binding", "../../validate/testdata/account.yaml", "-dir", filepath.Join(dir, "out"), log})
	if err != nil || code != 1 {
		t.Fatalf("exit %d, %v:\n%s", code, err, out)
	}
	var d validate.Document
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatalf("-json printed no JSON alone (%v):\n%s", err, out)
	}
	if d.Format != validate.ReportFormat || d.Command != "validate" || d.Verdict != "fail" || d.Exit != 1 || d.Reason != "rejects withdraw" ||
		len(d.Actors) != 1 || len(d.Actors[0].Failures) != 1 {
		t.Fatalf("the document:\n%s", out)
	}
	fl := d.Actors[0].Failures[0]
	if want := log + ":2"; fl.Record == nil || fl.Record.ID != want || fl.At != "record "+want || fl.Record.Process != "over" || fl.Kind != "disabled" {
		t.Errorf("the failing record, want %s:\n%s", want, out)
	}
	for _, p := range []string{junit, sarif} {
		b, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(b), "withdraw") {
			t.Errorf("%s: %v\n%s", p, err, b)
		}
	}
	es, err := os.ReadDir(reports)
	if err != nil || len(es) != 1 || !strings.HasSuffix(es[0].Name(), "-validate-"+strconv.Itoa(os.Getpid())+".json") {
		t.Errorf("GOTOGETHER_REPORT_DIR: %v %v", es, err)
	}
	// a -report of no known format is refused, naming them
	if code, _, err := validated([]string{"-report", "html=x", "-binding", "../../validate/testdata/account.yaml", log}); code != 2 || err == nil ||
		!strings.Contains(err.Error(), "json, junit, sarif") {
		t.Errorf("-report html=x: exit %d, %v", code, err)
	}
}

// simulate -json is its verdicts as checks, each invariant's and
// possible's with its detail (a counterexample's lines its own), the
// header and the graph's lines notes, and no text.
func TestSimulateJSON(t *testing.T) {
	skip.Quint(t) // not parallel: simulate prints to os.Stdout
	code, got := capture(t, func() (int, error) {
		return simulate([]string{"-json", "-graph", "-binding", "../../validate/testdata/vending.yaml"})
	})
	var d validate.Document
	if err := json.Unmarshal([]byte(got), &d); err != nil {
		t.Fatalf("exit %d, no JSON alone (%v):\n%s", code, err, got)
	}
	if d.Command != "simulate" || d.Exit != code || len(d.Checks) == 0 || len(d.Notes) == 0 || !strings.HasPrefix(d.Notes[0], "simulate ") {
		t.Errorf("the document:\n%s", got)
	}
	for _, c := range d.Checks {
		if c.Name == "" || c.Verdict == "" || c.Kind == "" {
			t.Errorf("a check unnamed or of no verdict: %+v", c)
		}
	}
}
