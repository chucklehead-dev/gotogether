package qtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// Under GOTOGETHER_EVIDENCE each Validate and Record run writes its
// manifest, run<N>.json, beside a copy of its step log, which it hashes:
// the instance checked, the verdict and failing step with its ref, and
// the driver and artifacts its options name. An exploration in the same
// test does not clear them (its evidence goes beside them), and a rerun
// of the test does.
func TestRunManifests(t *testing.T) {
	RequireQuint(t)
	dir := t.TempDir()
	t.Setenv("GOTOGETHER_EVIDENCE", dir)
	shot := filepath.Join(t.TempDir(), "42.webp")
	if err := os.WriteFile(shot, []byte("a screenshot"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := []qtrace.Step{{Action: "deposit", Recorder: "r", Seq: 1, Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(6)}, Ref: "trace.jsonl:42"}}
	v := Validate(t, "../validate/testdata/account.yaml", bad, Constants(map[string]string{"LIMIT": "120"}),
		Driver(validate.Driver{Name: "bombadil", Version: "0.3", Seed: "7", Args: []string{"--headless"}}),
		Artifacts(validate.Artifact{Name: "screenshot", Path: shot}))
	v.Expect(t, Mismatch("deposit"))
	r := Record(t, "../validate/testdata/account.yaml", func(rec *qobs.Recorder) {
		rec.Record(t.Context(), "deposit", nil, "amount", 5, "obs.balance", 5)
	})
	r.Expect(t, Conforms())
	System{Binding: "../validate/testdata/account.yaml", Run: race}.Explore(t, ExploreConfig{Seeds: 1, Chooser: func(uint64) qenv.Chooser { return qenv.Nominal{} }})
	at := filepath.Join(dir, pkgDir(), t.Name())
	m1, err := validate.ReadManifest(filepath.Join(at, "run1.json"))
	if err != nil {
		t.Fatal(err)
	}
	m2, err := validate.ReadManifest(filepath.Join(at, "run2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(at, "x1.json")); err != nil {
		t.Errorf("the exploration's evidence: %v", err)
	}
	if w, err := m1.Verify(); err != nil || len(w) > 0 {
		t.Errorf("verify: %v %v", w, err)
	}
	if m1.Name != t.Name() || m1.Package != "qtest" || m1.Import != "github.com/chucklehead-dev/gotogether/qtest" || m1.Model.Constants["LIMIT"] != "120" || m1.Driver == nil || m1.Driver.Seed != "7" ||
		len(m1.Artifacts) != 1 || m1.Artifacts[0].SHA256 == "" || m1.Tools.Quint == "" {
		t.Errorf("manifest: %+v", m1)
	}
	// its files bundled beside it: the directory travels
	bundled := filepath.Join(at, "run1")
	if len(m1.Inputs) != 1 || !strings.HasPrefix(m1.Inputs[0].Path, bundled) || m1.Inputs[0].Steps != 1 {
		t.Errorf("inputs: %+v", m1.Inputs)
	}
	if !strings.HasPrefix(m1.Model.Binding.Path, bundled) || !strings.HasPrefix(m1.Model.Spec.Path, bundled) || !strings.HasPrefix(m1.Artifacts[0].Path, bundled) {
		t.Errorf("not bundled: %+v %+v %+v", m1.Model.Binding, m1.Model.Spec, m1.Artifacts)
	}
	if f := m1.Verdict.Failing; m1.Verdict.Verdict != validate.Fail || m1.Verdict.Reason != "mismatch after deposit" || f == nil || f.Ref != "trace.jsonl:42" || f.Seq != 1 {
		t.Errorf("verdict: %+v", m1.Verdict)
	}
	if m2.Verdict.Verdict != validate.Pass || m2.Driver != nil {
		t.Errorf("Record's manifest: %+v", m2)
	}
	got, err := r.Manifest()
	if err != nil || got.Inputs[0].Path != r.StepLog || got.Verdict.Verdict != validate.Pass {
		t.Errorf("Run.Manifest: %+v, %v", got, err)
	}
	rerun(t) // the next run of the test: one run, no stale run2.json
	Validate(t, "../validate/testdata/account.yaml", bad)
	if _, err := os.Stat(filepath.Join(at, "run2.json")); !os.IsNotExist(err) {
		t.Errorf("a stale run2.json survived the rerun: %v", err)
	}
	if _, err := (&Run{}).Manifest(); err == nil || !strings.Contains(err.Error(), "only Validate's and Record's runs") {
		t.Errorf("a run of no manifest: %v", err)
	}
}

// A manifest's driver is of a run gotogether did not drive: an
// exploration refuses Driver and Artifacts, saying why.
func TestExploreRefusesDriver(t *testing.T) {
	t.Parallel()
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		System{Binding: "../validate/testdata/account.yaml", Run: race, Opts: []Option{Driver(validate.Driver{Name: "x"})}}.Explore(f, ExploreConfig{Seeds: 1})
	}()
	<-done
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "Driver() does nothing") || !strings.Contains(f.errs[0], "gotogether did not drive") {
		t.Errorf("%v", f.errs)
	}
}

// A manifest's run of no verdict is listed as no verdict, as an
// exploration's run of none is: not as failing.
func TestManifestEvidenceNoVerdict(t *testing.T) {
	t.Parallel()
	e := ManifestEvidence(&validate.Manifest{Name: "TestX", Verdict: validate.RunVerdict{Verdict: validate.NotEvaluated, Reason: "nothing was checked"}})
	if e.Conform != 0 || e.FailingBy["no verdict (nothing was checked)"] != 1 || e.Role != RoleExternal {
		t.Errorf("%+v", e)
	}
	e = ManifestEvidence(&validate.Manifest{Name: "TestX", Verdict: validate.RunVerdict{Verdict: validate.NotEvaluated, Reason: "no verdict (lost steps)"}})
	if e.FailingBy["no verdict (lost steps)"] != 1 {
		t.Errorf("%+v", e)
	}
}
