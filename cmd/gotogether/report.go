package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/validate"
)

// machineReports are the machine-readable reports a command writes
// besides, or instead of, its text (I20): -json prints the report as
// JSON (gotogether.report/2, validate.Document) in place of the text;
// -report FORMAT=FILE writes it to FILE as json, junit or sarif beside
// the text (repeatable); GOTOGETHER_REPORT_DIR=dir writes its JSON into
// dir, a file per command (the Action reads them for the job summary).
type machineReports struct {
	json  bool
	files repeated
	dir   string
}

// reportFormats are -report's formats, each the document's rendering.
// Read-only.
var reportFormats = map[string]func(*validate.Document) []byte{
	"json":  (*validate.Document).JSON,
	"junit": (*validate.Document).JUnit,
	"sarif": (*validate.Document).SARIF,
}

func (m *machineReports) flags(fset *flag.FlagSet) {
	fset.BoolVar(&m.json, "json", false, "print the report as JSON (gotogether.report/2: the verdict, each failure's record with its wall time, recorder and trace, the model's expectation and the state's diff) instead of the text")
	fset.Var(&m.files, "report", "FORMAT=FILE: also write the report to FILE as json (gotogether.report/2), junit (JUnit XML, a test case per check) or sarif (SARIF 2.1.0, a result per failure at its record's line); repeatable")
	fset.StringVar(&m.dir, "report-dir", "", "write a separate JSON report into DIR for each completed or failed analysis (overrides GOTOGETHER_REPORT_DIR)")
}

func (m *machineReports) directory() (string, string) {
	if m.dir != "" {
		return m.dir, "-report-dir=" + m.dir
	}
	dir := os.Getenv("GOTOGETHER_REPORT_DIR")
	return dir, "GOTOGETHER_REPORT_DIR=" + dir
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
	// Destinations are independent writes, not a multi-file transaction.
	// Finish file delivery before stdout so an agent sees the command's final
	// operational status, including a later destination's failure.
	recordError := func(werr error) {
		if werr == nil {
			return
		}
		err = errors.Join(err, werr)
		code, d.Exit, d.Verdict, d.Error = 2, 2, "error", err.Error()
	}
	type deliveredReport struct {
		path   string
		render func(*validate.Document) []byte
	}
	var delivered []deliveredReport
	deliveryFailed := false
	for _, f := range m.files {
		format, file, _ := strings.Cut(f, "=")
		if werr := os.WriteFile(file, reportFormats[format](d), 0o644); werr != nil {
			recordError(fmt.Errorf("-report %s: %w", f, werr))
			deliveryFailed = true
		} else {
			delivered = append(delivered, deliveredReport{file, reportFormats[format]})
		}
	}
	if dir, label := m.directory(); dir != "" {
		werr := os.MkdirAll(dir, 0o755)
		var path string
		if werr == nil {
			// A directory retains independent reports, even if clock readings repeat.
			// Never overwrite a previous analysis with a later binding's result.
			var file *os.File
			file, werr = os.CreateTemp(dir, fmt.Sprintf("%d-%s-%d-*.json", time.Now().UnixNano(), d.Command, os.Getpid()))
			if werr == nil {
				path = file.Name()
				_, werr = file.Write(d.JSON())
				werr = errors.Join(werr, file.Close())
				if werr != nil {
					werr = errors.Join(werr, os.Remove(path))
				}
			}
		}
		if werr != nil {
			recordError(fmt.Errorf("%s: %w", label, werr))
			deliveryFailed = true
		} else {
			delivered = append(delivered, deliveredReport{path, (*validate.Document).JSON})
		}
	}
	// Refresh successful destinations once to reflect later delivery failures.
	// A refresh can itself fail, leaving earlier bytes unchanged; its errors are
	// joined too; no retry loop or cross-destination atomicity is promised.
	refresh := func() {
		for _, report := range delivered {
			if werr := os.WriteFile(report.path, report.render(d), 0o644); werr != nil {
				recordError(fmt.Errorf("refresh report %s: %w", report.path, werr))
			}
		}
	}
	if deliveryFailed {
		refresh()
	}
	if m.json {
		if _, werr := stdout.Write(d.JSON()); werr != nil {
			recordError(fmt.Errorf("-json stdout: %w", werr))
			refresh()
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

// simPrint prints simulate's text to its log and, unless -json, stdout.
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
