package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/validate"
)

// machineReports are the machine-readable reports a command writes
// besides, or instead of, its text (I20): -json prints the report as
// JSON (gotogether.report/1, validate.Document) in place of the text;
// -report FORMAT=FILE writes it to FILE as json, junit or sarif beside
// the text (repeatable); GOTOGETHER_REPORT_DIR=dir writes its JSON into
// dir, a file per command (the Action reads them for the job summary).
type machineReports struct {
	json  bool
	files repeated
}

// reportFormats are -report's formats, each the document's rendering.
// Read-only.
var reportFormats = map[string]func(*validate.Document) []byte{
	"json":  (*validate.Document).JSON,
	"junit": (*validate.Document).JUnit,
	"sarif": (*validate.Document).SARIF,
}

func (m *machineReports) flags(fset *flag.FlagSet) {
	fset.BoolVar(&m.json, "json", false, "print the report as JSON (gotogether.report/1: the verdict, each failure's record with its wall time, recorder and trace, the model's expectation and the state's diff) instead of the text")
	fset.Var(&m.files, "report", "FORMAT=FILE: also write the report to FILE as json (gotogether.report/1), junit (JUnit XML, a test case per check) or sarif (SARIF 2.1.0, a result per failure at its record's line); repeatable")
}

// resolve checks the -report flags, and silences the text (*out) under
// -json.
func (m *machineReports) resolve(out *io.Writer) error {
	for _, f := range m.files {
		format, file, ok := strings.Cut(f, "=")
		if _, known := reportFormats[format]; !ok || file == "" || !known {
			return fmt.Errorf("-report %s: want FORMAT=FILE, FORMAT one of json, junit, sarif (-report junit=report.xml)", f)
		}
	}
	if m.json {
		*out = io.Discard
	}
	return nil
}

// write finishes d with the command's exit status and error, and writes
// the reports asked for: -json's to stdout, -report's files, and
// GOTOGETHER_REPORT_DIR's. It returns the command's status and error,
// or a report's write error (status 2).
func (m *machineReports) write(d *validate.Document, stdout io.Writer, code int, err error) (int, error) {
	d.Exit = code
	if err != nil {
		d.Error = err.Error()
	}
	switch {
	case err != nil:
		d.Verdict = "error"
	case code == 2 && d.Verdict == "":
		d.Verdict = "error"
	case d.Verdict == "" && code == 0:
		d.Verdict = "pass"
	case d.Verdict == "":
		d.Verdict = "fail"
	}
	if m.json {
		if _, werr := stdout.Write(d.JSON()); werr != nil && err == nil {
			return 2, werr
		}
	}
	for _, f := range m.files {
		format, file, _ := strings.Cut(f, "=")
		if werr := os.WriteFile(file, reportFormats[format](d), 0o644); werr != nil && err == nil {
			return 2, fmt.Errorf("-report %s: %w", f, werr)
		}
	}
	if dir := os.Getenv("GOTOGETHER_REPORT_DIR"); dir != "" {
		name := fmt.Sprintf("%d-%s-%d.json", time.Now().UnixNano(), d.Command, os.Getpid()) // in the order run
		werr := os.MkdirAll(dir, 0o755)
		if werr == nil {
			werr = os.WriteFile(filepath.Join(dir, name), d.JSON(), 0o644)
		}
		if werr != nil && err == nil {
			return 2, fmt.Errorf("GOTOGETHER_REPORT_DIR=%s: %w", dir, werr)
		}
	}
	return code, err
}

// simLog retains display text and the native simulation document. Display
// wording is never parsed to infer machine-readable checks or scope.
type simLog struct {
	result  *validate.Document
	text    strings.Builder
	quiet   bool // -json: the text is the document's, not printed
	binding string
}

// Process-wide: the running simulate command's log (one per process:
// simulate's tests capture os.Stdout and do not run in parallel), nil
// when none runs.
var simCapture *simLog

// simPrintf prints a line of simulate's report: to os.Stdout (as its
// tests capture it), unless -json, and to the running command's log.
func simPrintf(format string, a ...any) { simPrint(fmt.Sprintf(format, a...)) }

// simPrint is simPrintf of s, as it is.
func simPrint(s string) {
	if l := simCapture; l != nil {
		l.text.WriteString(s)
		if l.quiet {
			return
		}
	}
	os.Stdout.WriteString(s)
}

// document returns native facts when simulation completed, and only contextual
// text when a tool failure prevented a native report from being produced.
func (l *simLog) document() *validate.Document {
	if l.result != nil {
		return l.result
	}
	d := validate.NewDocument("simulate")
	d.Binding = l.binding
	if l.text.Len() > 0 {
		d.Notes = append(d.Notes, strings.TrimSpace(l.text.String()))
	}
	return d
}
