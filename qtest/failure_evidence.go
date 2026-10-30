package qtest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate/runprofile"
)

// failedEvidence retains execution facts when a failure stopped the exploration
// before model validation. None of these histories has a model verdict.
func (x *Exploration) failedEvidence(origins []origin) *Evidence {
	e := &Evidence{Format: EvidenceFormat, Role: x.role(), Session: os.Getenv("GOTOGETHER_SESSION"), Package: pkgDir(), Import: importPath(), Test: x.ref(), Spec: x.model, Mutant: x.mutation,
		Runs: len(x.Runs), Unjudged: len(x.Runs), FailingBy: map[string]int{}, RunErrors: map[string]string{}}
	if x.profile != nil {
		e.Profiles = append(e.Profiles, x.profile.Clone())
	}
	for i, r := range x.Runs {
		if r == nil {
			missing := &Run{origin: origins[i]}
			e.RunErrors[x.ref()+":"+missing.ID()] = "scenario failed the test before returning"
			continue
		}
		name := x.ref() + ":" + r.ID()
		if r.Err != nil {
			e.RunErrors[name] = r.Err.Error()
		} else {
			e.RunErrors[name] = "not judged: exploration stopped before model validation; see test diagnostics"
		}
		if len(r.simulations) > 0 {
			e.Executions = append(e.Executions, (runprofile.Execution{Run: name, Simulations: r.simulations}).Clone())
		}
	}
	return e
}

// writeEvidence uses the same destination for judged and unjudged executions.
func (x *Exploration) writeEvidence(t testing.TB, evidence *Evidence) {
	t.Helper()
	if dir := evidenceDir(x.test); dir != "" {
		err := os.MkdirAll(dir, 0o755)
		if err == nil {
			err = evidence.WriteJSON(filepath.Join(dir, fmt.Sprintf("x%d.json", x.id)))
		}
		if err != nil {
			t.Logf("gotogether: evidence: %v", err)
		}
	} else if os.Getenv("GOTOGETHER_KEEP") != "" {
		if err := os.MkdirAll(x.dir, 0o755); err != nil {
			t.Logf("gotogether: evidence.json: %v", err)
			return
		}
		if err := evidence.WriteJSON(filepath.Join(x.dir, "evidence.json")); err != nil {
			t.Logf("gotogether: evidence.json: %v", err)
		}
	}
}
