package qtest

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate"
)

// Driver names what drove the run Validate or Record checks (an external
// explorer, a load generator, a replayed production session), for the
// run's manifest (Run.Manifest): its name, version, seed, where it ran
// and its arguments. gotogether drove none of it, so only the caller
// can say.
func Driver(d validate.Driver) Option {
	return option(optDriver, func(c *settings) { c.driver = &d })
}

// Artifacts names the driver's records of the run Validate or Record
// checks (its trace, screenshots), for the run's manifest: each is hashed
// when the manifest is made. Paths are the test's (relative to its
// package's directory).
func Artifacts(a ...validate.Artifact) Option {
	return option(optArtifacts, func(c *settings) { c.artifacts = append(c.artifacts, a...) })
}

// Manifest is the run's manifest (validate.Manifest, gotogether.run/1):
// the tools, the instance it was checked against (the binding and spec
// hashed, the constants and invariants), its step log hashed with its
// losses, the verdict and failing step, and the driver and artifacts its
// options named (Driver, Artifacts). `gotogether validate -replay` judges
// it again. Validate's and Record's runs have one: an exploration's runs
// are reproduced by GOTOGETHER_RUN instead.
//
// Under GOTOGETHER_EVIDENCE each such run writes its manifest itself, as
// dir/<package>/<test>/run<N>.json (N counting the test's runs from 1),
// with copies of the files it names in run<N>/ (validate.Manifest.Bundle:
// its step log, its binding with the spec and the files the spec imports
// at their places, its artifacts), so the directory travels: a CI job's
// uploaded evidence replays on another machine. The step log is the
// run's whole and unsanitized: mind where the directory is uploaded.
func (r *Run) Manifest() (*validate.Manifest, error) {
	if r.checker == nil || r.StepLog == "" {
		return nil, errors.New("gotogether: the run has no manifest: only Validate's and Record's runs have one (an exploration's are reproduced by GOTOGETHER_RUN)")
	}
	return r.manifest(r.StepLog)
}

// manifest is the run's manifest with log, its steps, as its input.
func (r *Run) manifest(log string) (*validate.Manifest, error) {
	var results []*validate.Result
	if r.Report != nil {
		results = r.Report.Actors
	}
	m, err := r.checker.Manifest(results, r.search)
	if err != nil {
		return nil, err
	}
	m.Name, m.Package, m.Import, m.Driver = r.name, pkgDir(), importPath(), r.driver
	if err := m.AddInput(log, "steps", r.Recorded); err != nil {
		return nil, err
	}
	for _, a := range r.artifacts {
		if err := m.AddArtifact(a.Name, a.Path); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// writeManifest writes run's manifest under GOTOGETHER_EVIDENCE, if set,
// with its files bundled beside it (Run.Manifest says where). A manifest
// it cannot write is logged, as an exploration's evidence is: the check
// itself stands.
func writeManifest(t testing.TB, run *Run) {
	dir := evidenceDir(t.Name())
	if dir == "" {
		return
	}
	n := nextRun(t)
	m, err := run.manifest(run.StepLog)
	if err == nil {
		err = m.Bundle(filepath.Join(dir, fmt.Sprintf("run%d", n)))
	}
	if err == nil {
		err = m.Write(filepath.Join(dir, fmt.Sprintf("run%d.json", n)))
	}
	if err != nil {
		t.Logf("gotogether: manifest: %v", err)
	}
}
