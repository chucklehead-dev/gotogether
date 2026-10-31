package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/chucklehead-dev/gotogether/internal/atomicdir"
	"github.com/chucklehead-dev/gotogether/validate"
)

// bundleTo exports checked evidence without rerunning or changing its judgment.
// The manifest and its files are published together, using the native format.
func bundleTo(out io.Writer, args []string) error {
	fset := commandFlags("bundle")
	directory := fset.String("out", "", "fresh directory for run.json and its verified files; existing paths are refused")
	jsonOut := fset.Bool("json", false, "write the exported native run manifest to stdout (relative paths resolve beside run.json)")
	fset.Parse(args)
	if *directory == "" || fset.NArg() != 1 {
		return needs("bundle", "-out DIR and exactly one run manifest")
	}
	m, err := validate.ReadManifest(fset.Arg(0))
	if err != nil {
		return err
	}
	dest, err := filepath.Abs(*directory)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("bundle: %s already exists: choose a fresh -out directory", dest)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".gotogether-bundle-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := m.Bundle(filepath.Join(stage, "files")); err != nil {
		return err
	}
	manifestPath := filepath.Join(stage, "run.json")
	if err := m.Write(manifestPath); err != nil {
		return err
	}
	// Check the written manifest, not just its in-memory precursor. This is
	// export integrity; no model verdict is inferred from this check.
	written, err := validate.ReadManifest(manifestPath)
	if err != nil {
		return err
	}
	warnings, err := written.Verify()
	if err != nil {
		return err
	}
	if len(warnings) != 0 {
		return fmt.Errorf("bundle: exported evidence did not verify: %v", warnings)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	if err := atomicdir.Publish(stage, dest); err != nil {
		return fmt.Errorf("bundle: publish %s: %w", dest, err)
	}
	if *jsonOut {
		_, err = out.Write(data)
		return err
	}
	_, err = fmt.Fprintf(out, "bundled verified evidence to %s\nRecorded verdict: %s (unchanged; export does not judge the model).\n", filepath.Join(dest, "run.json"), written.Verdict.Verdict)
	if err != nil {
		return err
	}
	if written.Receipt != nil && written.Receipt.Composition != nil {
		_, err = fmt.Fprintln(out, "Composition evidence: rerun the originating registered System/test to reproduce its checks; primary-binding replay cannot reproduce registrations.")
	} else {
		_, err = fmt.Fprintf(out, "Replay supported histories: gotogether validate -replay %q\n", filepath.Join(dest, "run.json"))
	}
	return err
}
