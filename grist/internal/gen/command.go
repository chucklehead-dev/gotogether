package gen

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// PreflightError means Command already rendered a failing maintenance report.
// Error retains its full detail for programmatic MainArgs callers; Main need
// only return a nonzero exit status, without printing the same report twice.
type PreflightError struct {
	Report *Preflight
	Cause  error
}

func (e *PreflightError) Error() string {
	s := e.Report.String()
	if e.Cause != nil {
		s += "\nmodel maintenance failed: " + e.Cause.Error()
	}
	return s
}

// Command runs the app's model maintenance workflow. Check and JSON modes are
// nonwriting inspections. Ordinary generation applies only a safe mechanical
// plan; init additionally creates missing handwritten starter files, never
// overwriting one. All paths share the same preflight findings and readiness.
func Command(app App, args []string, stdout io.Writer, usage func(msg string) error) error {
	name := app.Catalog().Name
	fs := flag.NewFlagSet(name+" model", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", ".", "")
	check := fs.Bool("check", false, "")
	jsonOutput := fs.Bool("json", false, "")
	init := fs.Bool("init", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return usage(fmt.Sprintf("usage: %s model [-dir DIR] [-check] [-json] [-init]; -json implies a nonwriting check", name))
	}
	if *init && (*check || *jsonOutput) {
		return usage("model -init writes starter files and cannot be combined with the read-only -check or -json modes")
	}
	preflight := Inspect(app, *dir)
	if *check || *jsonOutput {
		return renderPreflight(stdout, preflight, *jsonOutput, nil)
	}
	if !preflight.CanGenerate() {
		return renderPreflight(stdout, preflight, false, nil)
	}
	if *init {
		created, err := initializeModel(app, preflight.dir)
		for _, rel := range created {
			if _, writeErr := fmt.Fprintf(stdout, "wrote %s (starter only: write and validate the application's rules and invariants)\n", filepath.Join(*dir, rel)); writeErr != nil {
				return writeErr
			}
		}
		before := preflight
		preflight = Inspect(app, *dir)
		preflight.RetainWarnings(before)
		if err != nil {
			return renderPreflight(stdout, preflight, false, err)
		}
		if !preflight.CanGenerate() {
			return renderPreflight(stdout, preflight, false, nil)
		}
	}
	changes := append([]Change(nil), preflight.Changes...)
	post, err := preflight.Apply()
	if err == nil && len(changes) > 0 {
		for _, c := range changes {
			if _, writeErr := fmt.Fprintf(stdout, "%s generated %s\n", c.Operation, c.Path); writeErr != nil {
				return writeErr
			}
		}
	}
	return renderPreflight(stdout, post, false, err)
}

func renderPreflight(stdout io.Writer, report *Preflight, asJSON bool, cause error) error {
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(stdout, report.String()); err != nil {
			return err
		}
		if cause != nil {
			if _, err := fmt.Fprintf(stdout, "model maintenance failed: %v\n", cause); err != nil {
				return err
			}
		}
	}
	if cause != nil || !report.OK() {
		return &PreflightError{Report: report, Cause: cause}
	}
	return nil
}

func initializeModel(app App, dir string) ([]string, error) {
	files, err := Starter(app)
	if err != nil {
		return nil, err
	}
	name := app.Catalog().Name
	var created []string
	for _, rel := range []string{rulesFile(name), modelFile(name)} {
		if err := safeTarget(dir, rel); err != nil {
			return created, err
		}
	}
	for _, rel := range []string{rulesFile(name), modelFile(name)} {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return created, err
		}
		// Exclusive creation makes absence a condition of the write itself.
		// A handwritten file appearing since preflight is never overwritten.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return created, err
		}
		_, writeErr := f.Write(files[rel])
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			return created, fmt.Errorf("created %s but its starter may be incomplete: %w; inspect the file before retrying", path, errors.Join(writeErr, closeErr))
		}
		created = append(created, rel)
	}
	return created, nil
}

func mustQuint(name string) string { q, _ := QuintName(name); return q }
