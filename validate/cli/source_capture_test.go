package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A cached checker may judge old source correctly, but must never identify its
// result with replacement source when the run is subsequently captured.
func TestSourceCaptureJudgedDependencies(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	for _, dependency := range []string{"binding", "spec", "import"} {
		t.Run(dependency, func(t *testing.T) {
			dir := t.TempDir()
			sourceCaptureCopy(t, "../testdata/account.yaml", filepath.Join(dir, "account.yaml"))
			sourceCaptureCopy(t, "../testdata/account.qnt", filepath.Join(dir, "account.qnt"))
			binding := filepath.Join(dir, "account.yaml")
			path := binding
			old, replacement := "LIMIT: '100'", "LIMIT: '1'"
			if dependency == "spec" {
				path = filepath.Join(dir, "account.qnt")
				old, replacement = "balance <= LIMIT", "balance <= 1"
			}
			if dependency == "import" {
				spec := filepath.Join(dir, "account.qnt")
				body := sourceCaptureRead(t, spec)
				body = bytes.Replace(body, []byte("module account {"), []byte("module account {\n  import bounds.* from \"./bounds\""), 1)
				body = bytes.Replace(body, []byte("balance <= LIMIT"), []byte("balance <= allowedBalance"), 1)
				sourceCaptureWrite(t, spec, body)
				path = filepath.Join(dir, "bounds.qnt")
				sourceCaptureWrite(t, path, []byte("module bounds { pure val allowedBalance = 100 }\n"))
				old, replacement = "= 100", "= 1"
			}
			before := sourceCaptureRead(t, path)
			changed := bytes.Replace(before, []byte(old), []byte(replacement), 1)
			if bytes.Equal(before, changed) {
				t.Fatalf("mutation did not match %s", path)
			}
			log := filepath.Join(dir, "run.jsonl")
			inputCaptureWriteSteps(t, log, false, inputCaptureStep(1, "deposit", 5, 5))
			check := Check{Binding: binding, Files: []string{log}, Options: validate.Options{Dir: t.TempDir(), NoITF: true}}
			// Populate the model/checker caches, then obtain the judgment under test.
			warm := sourceCaptureJudge(t, check, true)
			warm.Close()
			j := sourceCaptureJudge(t, check, true)
			defer j.Close()
			inputCaptureManifest(t, j)
			sourceCaptureWrite(t, path, changed)
			sourceCaptureRefused(t, j, path)
			check.Options.Dir = t.TempDir()
			now := sourceCaptureJudge(t, check, false)
			now.Close()
			sourceCaptureWrite(t, path, before)
			inputCaptureManifest(t, j)
		})
	}
}

// Empty histories have no actor Result to carry provenance. The high-level
// judgment must still retain and verify the checker source before capture.
func TestSourceCaptureEmptyHistory(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	dir := t.TempDir()
	binding := filepath.Join(dir, "account.yaml")
	spec := filepath.Join(dir, "account.qnt")
	sourceCaptureCopy(t, "../testdata/account.yaml", binding)
	sourceCaptureCopy(t, "../testdata/account.qnt", spec)
	j, err := (Check{Binding: binding, Options: validate.Options{Dir: t.TempDir(), NoITF: true}}).Judge(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if j.Checker == nil || len(j.Results) != 0 {
		t.Fatalf("expected a checker with no actor results: checker=%v results=%d", j.Checker, len(j.Results))
	}
	inputCaptureManifest(t, j)
	before := sourceCaptureRead(t, spec)
	sourceCaptureWrite(t, spec, append(bytes.Clone(before), '\n'))
	m, err := j.Manifest()
	if m != nil || err == nil || !strings.Contains(err.Error(), spec) {
		t.Fatalf("empty judgment accepted changed source: manifest=%+v error=%v", m, err)
	}
	sourceCaptureWrite(t, spec, before)
	inputCaptureManifest(t, j)
}

func TestSourceCaptureConversionRules(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	binding, _ := inputCaptureAccount(t)
	dir := t.TempDir()
	rules := filepath.Join(dir, "convert.yaml")
	before := []byte("actor: resource:service.name\nrules:\n  - span: Bank/Deposit\n    action: deposit\n    args: { amount: attr:amount|int }\n    obs: { balance: attr:balance|int }\n")
	sourceCaptureWrite(t, rules, before)
	path := filepath.Join(dir, "spans.json")
	sourceCaptureWrite(t, path, []byte(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"account"}}]},"scopeSpans":[{"spans":[{"traceId":"00000000000000000000000000000001","spanId":"0000000000000001","name":"Bank/Deposit","startTimeUnixNano":"1000000000","endTimeUnixNano":"1000000001","attributes":[{"key":"amount","value":{"intValue":"5"}},{"key":"balance","value":{"intValue":"5"}}]}]}]}]}`))
	check := Check{Binding: binding, Convert: rules, Files: []string{path}, Options: validate.Options{Dir: t.TempDir(), NoITF: true}}
	j := sourceCaptureJudge(t, check, true)
	defer j.Close()
	inputCaptureManifest(t, j)
	sourceCaptureWrite(t, rules, bytes.Replace(before, []byte("action: deposit"), []byte("action: withdraw"), 1))
	sourceCaptureRefused(t, j, rules)
	check.Options.Dir = t.TempDir()
	now := sourceCaptureJudge(t, check, false)
	now.Close()
	sourceCaptureWrite(t, rules, before)
	inputCaptureManifest(t, j)
}

// ITF-only judgments do not construct a primary Checker. Their schema/model and
// optional binding still need the same capture protection as JSONL judgments.
func TestSourceCaptureTypedITF(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	for _, dependency := range []string{"binding", "spec"} {
		t.Run(dependency, func(t *testing.T) {
			dir := t.TempDir()
			spec := filepath.Join(dir, "bignumber.qnt")
			sourceCaptureCopy(t, "../../itf/testdata/bignumber.qnt", spec)
			binding := filepath.Join(dir, "big.yaml")
			sourceCaptureWrite(t, binding, []byte("spec: bignumber.qnt\nmodule: bignumber\nactions:\n  step: {}\n"))
			trace := filepath.Join(dir, "typed.itf.json")
			sourceCaptureCopy(t, "../../itf/testdata/bignumber.rust.itf.json", trace)
			j := sourceCaptureJudge(t, Check{Binding: binding, Files: []string{trace}, Options: validate.Options{Dir: t.TempDir(), NoITF: true, Backend: "typescript"}}, true)
			defer j.Close()
			if j.Checker != nil || len(j.ITF) != 1 {
				t.Fatalf("expected an ITF-only judgment: checker=%v ITF=%d", j.Checker, len(j.ITF))
			}
			inputCaptureManifest(t, j)
			path := binding
			if dependency == "spec" {
				path = spec
			}
			before := sourceCaptureRead(t, path)
			sourceCaptureWrite(t, path, append(bytes.Clone(before), '\n'))
			sourceCaptureRefused(t, j, path)
			sourceCaptureWrite(t, path, before)
			inputCaptureManifest(t, j)
		})
	}
}

func sourceCaptureJudge(t *testing.T, check Check, pass bool) *Judged {
	t.Helper()
	j, err := check.Judge(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := validate.Fail
	if pass {
		want = validate.Pass
	}
	if j.OK != pass || j.Verdict.Verdict != want {
		j.Close()
		t.Fatalf("judgment=%+v OK=%v want=%s", j.Verdict, j.OK, want)
	}
	return j
}
func sourceCaptureRefused(t *testing.T, j *Judged, path string) {
	t.Helper()
	m, err := j.Manifest()
	if m != nil || err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("changed source attributed to old judgment: manifest=%+v error=%v (changed %s)", m, err, path)
	}
	if !j.OK || j.Verdict.Verdict != validate.Pass {
		t.Fatalf("refusing capture rewrote judgment: %+v", j.Verdict)
	}
}
func sourceCaptureRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func sourceCaptureWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func sourceCaptureCopy(t *testing.T, from, to string) {
	t.Helper()
	sourceCaptureWrite(t, to, sourceCaptureRead(t, from))
}
