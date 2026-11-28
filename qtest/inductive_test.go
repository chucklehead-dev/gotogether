package qtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// veilTB is a failTB that records a skip instead of skipping the test.
type veilTB struct {
	*failTB
	skipped string
}

func (s *veilTB) Skip(a ...any) { s.skipped = fmt.Sprint(a...); runtime.Goexit() }

// fakeVeilOnPath puts first on PATH a quint 0.35 whose Veil backend
// answers with validate/model's captured fixtures, by inductive
// invariant, and that hands everything else to the real quint.
func fakeVeilOnPath(t *testing.T, ready bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake Quint uses POSIX shell")
	}
	real, err := exec.LookPath("quint")
	if err != nil {
		RequireQuint(t)
	}
	fixtures, _ := filepath.Abs("../validate/model/testdata/veil")
	status := "echo 'not installed /home/u/.quint/veil/w'; exit 1"
	if ready {
		status = "echo 'ready /home/u/.quint/veil/w'; exit 0"
	}
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then sleep ${FAKE_VERSION_SLEEP:-0}; echo 0.35.0; exit 0; fi\n" +
		"if [ \"$1\" = veil ]; then " + status + "; fi\n" +
		"if [ \"$1\" = verify ] && [ \"$3\" = veil ]; then\n inv=''; out=''; itf=''\n" +
		" while [ $# -gt 0 ]; do case \"$1\" in --inductive-invariant) inv=$2; shift;; --out) out=$2; shift;; --out-itf) itf=$2; shift;; esac; shift; done\n" +
		" case \"$inv\" in\n" +
		"  nonNegative) cp " + fixtures + "/bank.json \"$out\"; exit 0;;\n" +
		"  'xNonNegative and yNonNegative') cp " + fixtures + "/lemma.json \"$out\"; exit 0;;\n" +
		"  xNonNegative) cp " + fixtures + "/cti.json \"$out\"; cp " + fixtures + "/cti.itf.json \"$itf\"; exit 1;;\n" +
		"  exactlyOnce) cp " + fixtures + "/refused.json \"$out\"; exit 1;;\n" +
		"  'xNonNegative and xSmall') cp " + fixtures + "/nopost.json \"$out\"; exit 1;;\n" +
		" esac\n exit 3\nfi\nexec " + real + " \"$@\"\n"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "quint"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

func inductiveExpect(t *testing.T, b string, claims ...ModelClaim) *veilTB {
	t.Helper()
	f := &veilTB{failTB: &failTB{TB: t}}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf and veilTB's Skip end the goroutine (runtime.Goexit)
		defer close(done)
		Model(t, b).Expect(f, claims...)
	}()
	<-done
	return f
}

// Inductive's outcomes in a test: proved holds; a counterexample to
// induction has no verdict, failing the test with both states; a lemma
// given by With proves it; a refusal and a machine with no Veil skip the
// test, never fail it, and a failing claim beside them still fails it.
func TestInductiveClaims(t *testing.T) {
	fakeVeilOnPath(t, true)
	const dir = "../validate/model/testdata/veil/"
	f := inductiveExpect(t, dir+"bank.yaml", Inductive("nonNegative"))
	if f.failed || f.skipped != "" || !strings.Contains(strings.Join(f.logs, "\n"), "the model proving nonNegative by induction: proved   nonNegative: inductive") {
		t.Errorf("bank: %v %q %v", f.errs, f.skipped, f.logs)
	}
	f = inductiveExpect(t, dir+"induct.yaml", Inductive("xNonNegative"))
	if !f.failed || !strings.Contains(strings.Join(f.errs, "\n"), "the model has no verdict on proving xNonNegative by induction: no verdict xNonNegative: not inductive: a counterexample to induction after shift") ||
		!strings.Contains(strings.Join(f.errs, "\n"), "after shift: x = -1, y = 0, settled = true") {
		t.Errorf("cti: %v", f.errs)
	}
	f = inductiveExpect(t, dir+"induct.yaml", Inductive("xNonNegative").With("yNonNegative"))
	if f.failed || f.skipped != "" {
		t.Errorf("with the lemma: %v %q", f.errs, f.skipped)
	}
	f = inductiveExpect(t, dir+"induct.yaml", Inductive("xNonNegative").With("xSmall"))
	if !f.failed || f.skipped != "" || !strings.Contains(strings.Join(f.errs, "\n"), "the model has no verdict on proving xNonNegative by induction with xSmall: no verdict xNonNegative with xSmall: not inductive: a counterexample to induction after shift") ||
		!strings.Contains(strings.Join(f.errs, "\n"), "its states: Veil could not show them: the model has no post-state; a complete transition witness could not be decoded") {
		t.Errorf("a counterexample Veil cannot show: %v %q", f.errs, f.skipped)
	}
	f = inductiveExpect(t, dir+"refused.yaml", Inductive("exactlyOnce"))
	if f.failed || !strings.Contains(f.skipped, "proving exactlyOnce by induction: refused by Veil, not in its fragment: the size of a set whose elements are not known is not supported") {
		t.Errorf("refused: %v %q", f.errs, f.skipped)
	}
	f = inductiveExpect(t, dir+"induct.yaml", Inductive("xNonNegative"), Inductive("xNonNegative").With("yNonNegative"))
	if !f.failed {
		t.Errorf("a counterexample beside a proof: %v", f.logs)
	}
	f = inductiveExpect(t, dir+"induct.yaml", Inductive("noSuch"))
	if !f.failed || !strings.Contains(strings.Join(f.errs, "\n"), "noSuch is no invariant of the binding") {
		t.Errorf("unknown: %v", f.errs)
	}
}

func TestInductiveClaimNotDone(t *testing.T) {
	fakeVeilOnPath(t, false)
	f := inductiveExpect(t, "../validate/model/testdata/veil/bank.yaml", Inductive("nonNegative"))
	if f.failed || !strings.Contains(f.skipped, "not done: Veil is not installed") || !strings.Contains(f.skipped, "quint veil install") {
		t.Errorf("not done: %v %q", f.errs, f.skipped)
	}
}

// A quint too slow to answer --version (a loaded machine) fails the
// claim, naming the timeout: never a skip, as a missing quint's would be.
func TestInductiveClaimProbeTimeout(t *testing.T) {
	fakeVeilOnPath(t, true)
	t.Setenv("FAKE_VERSION_SLEEP", "3")
	t.Setenv("GOTOGETHER_QUINT_PROBE_TIMEOUT", "300ms")
	f := inductiveExpect(t, "../validate/model/testdata/veil/bank.yaml", Inductive("nonNegative"))
	errs := strings.Join(f.errs, "\n")
	if !f.failed || f.skipped != "" || !strings.Contains(errs, "simulate -inductive: cannot ask quint whether Veil is installed: quint --version did not answer within 300ms (GOTOGETHER_QUINT_PROBE_TIMEOUT=300ms)") {
		t.Errorf("a slow quint: failed %v, skipped %q, %v", f.failed, f.skipped, f.errs)
	}
}
