package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A live log can grow while the checker is working. Hashing it only when the
// manifest is requested would attach an earlier PASS to a later illegal history.
// Cosmetic changes matter too: the fingerprint must identify the bytes parsed,
// not a fresh parsing that happens to produce the same verdict.
func TestInputCaptureFreezesJudgedBytes(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	binding, artifact := inputCaptureAccount(t)
	for _, compiled := range []bool{false, true} {
		mode := "source"
		if compiled {
			mode = "compiled"
		}
		for _, mutation := range []string{"append-illegal", "append-whitespace"} {
			t.Run(mode+"/"+mutation, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "live.jsonl")
				inputCaptureWriteSteps(t, path, false, inputCaptureStep(1, "deposit", 5, 5))
				check := Check{Binding: binding, Files: []string{path}, Options: validate.Options{Dir: t.TempDir(), NoITF: true}}
				if compiled {
					check.Compiled = artifact
				}
				j, err := check.Judge(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer j.Close()
				if !j.OK || j.Verdict.Verdict != validate.Pass || len(j.Inputs) != 1 || len(j.Inputs[0].Steps) != 1 {
					t.Fatalf("initial judgment: %+v %v", j.Verdict, err)
				}
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				inputCaptureManifest(t, j)
				if mutation == "append-illegal" {
					inputCaptureWriteSteps(t, path, true, inputCaptureStep(2, "withdraw", 9, -4))
				} else {
					f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := f.WriteString("\n  \n"); err != nil {
						t.Fatal(err)
					}
					if err := f.Close(); err != nil {
						t.Fatal(err)
					}
				}
				inputCaptureRefused(t, j, path)
				// Prove the append reached the actual parser/model, rather than merely
				// corrupting a fixture that no longer describes a checkable execution.
				check.Options.Dir = t.TempDir()
				now, err := check.Judge(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer now.Close()
				want := validate.Pass
				if mutation == "append-illegal" {
					want = validate.Fail
				}
				if now.Verdict.Verdict != want {
					t.Fatalf("changed input verdict=%s want=%s", now.Verdict, want)
				}
				if err := os.WriteFile(path, before, 0600); err != nil {
					t.Fatal(err)
				}
				// Refusal is not destructive: restoring the exact read bytes makes the
				// original judgment capturable again, independently of inode or mtime.
				inputCaptureManifest(t, j)
			})
		}
	}
}

// Conversion consumes raw OTLP bytes through a separate read path. The converted
// steps alone cannot establish the identity of the input file they came from.
func TestInputCaptureConvertedOTLP(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	binding, artifact := inputCaptureAccount(t)
	rules := filepath.Join(t.TempDir(), "convert.yaml")
	if err := os.WriteFile(rules, []byte("actor: resource:service.name\nrules:\n  - span: Bank/Deposit\n    action: deposit\n    args: { amount: attr:amount|int }\n    obs: { balance: attr:balance|int }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	const raw = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"account"}}]},"scopeSpans":[{"scope":{"name":"app"},"spans":[{"traceId":"00000000000000000000000000000001","spanId":"0000000000000001","name":"Bank/Deposit","startTimeUnixNano":"1000000000","endTimeUnixNano":"1000000001","attributes":[{"key":"amount","value":{"intValue":"5"}},{"key":"balance","value":{"intValue":"5"}}]}]}]}]}`
	for _, compiled := range []bool{false, true} {
		mode := "source"
		if compiled {
			mode = "compiled"
		}
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "app-spans.json")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			check := Check{Binding: binding, Convert: rules, Files: []string{path}, Options: validate.Options{Dir: t.TempDir(), NoITF: true}}
			if compiled {
				check.Compiled = artifact
			}
			j, err := check.Judge(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			if !j.OK || len(j.Inputs) != 1 || !j.Inputs[0].Converted || j.Inputs[0].Format != input.OTLP || len(j.Inputs[0].Steps) != 1 {
				t.Fatalf("converted judgment: %+v inputs=%+v", j.Verdict, j.Inputs)
			}
			inputCaptureManifest(t, j)
			if err := os.WriteFile(path, []byte(raw+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			inputCaptureRefused(t, j, path)
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			inputCaptureManifest(t, j)
		})
	}
}

// An ambiguous Rust ITF integer is re-read using its model's schema. That path
// must capture the bytes it actually parses, not lose provenance while typing.
func TestInputCaptureTypedITF(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	spec, err := filepath.Abs("../../itf/testdata/bignumber.qnt")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	binding := filepath.Join(dir, "big.yaml")
	if err := os.WriteFile(binding, []byte("spec: "+spec+"\nmodule: bignumber\nactions:\n  step: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile("../../itf/testdata/bignumber.rust.itf.json")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "typed.itf.json")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	j, err := (Check{Binding: binding, Files: []string{path}, Options: validate.Options{Dir: t.TempDir(), NoITF: true, Backend: "typescript"}}).Judge(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if !j.OK || len(j.ITF) != 1 || len(j.Inputs) != 1 || j.Inputs[0].Format != input.ITF {
		t.Fatalf("typed ITF: %+v inputs=%+v", j.Verdict, j.Inputs)
	}
	inputCaptureManifest(t, j)
	if err := os.WriteFile(path, append(bytes.Clone(before), '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	inputCaptureRefused(t, j, path)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	inputCaptureManifest(t, j)
}

func inputCaptureAccount(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"account.yaml", "account.qnt"} {
		b, err := os.ReadFile(filepath.Join("../testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	binding, artifact := filepath.Join(dir, "account.yaml"), filepath.Join(dir, "account.model.json")
	cm, err := validate.Compile(binding, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.Write(artifact); err != nil {
		t.Fatal(err)
	}
	return binding, artifact
}

func inputCaptureStep(seq uint64, action string, amount, balance int64) qtrace.Step {
	return qtrace.Step{Actor: "account", Recorder: "input-capture", Thread: "client", Seq: seq, Action: action, Args: map[string]any{"amount": amount}, Obs: map[string]any{"balance": balance}}
}

func inputCaptureWriteSteps(t *testing.T, path string, appendOnly bool, steps ...qtrace.Step) {
	t.Helper()
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if appendOnly {
		flags = os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := qtrace.WriteJSONL(f, steps); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func inputCaptureManifest(t *testing.T, j *Judged) {
	t.Helper()
	m, err := j.Manifest()
	if err != nil {
		t.Fatalf("unchanged read bytes refused: %v", err)
	}
	if warnings, err := m.Verify(); err != nil || len(warnings) != 0 {
		t.Fatalf("captured manifest verification: %v %v", warnings, err)
	}
	if m.Verdict.Verdict != j.Verdict.Verdict {
		t.Fatalf("capture changed verdict: %s -> %s", j.Verdict, m.Verdict)
	}
}

func inputCaptureRefused(t *testing.T, j *Judged, path string) {
	t.Helper()
	m, err := j.Manifest()
	if err == nil || m != nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed input was reattributed to earlier judgment: manifest=%+v error=%v", m, err)
	}
	if !j.OK || j.Verdict.Verdict != validate.Pass {
		t.Fatalf("refusing capture rewrote original judgment: %+v", j.Verdict)
	}
}

// Rebuilding the public input DTO must not silently opt a high-level judgment
// into the low-level RunRecord contract that trusts caller-supplied provenance.
func TestInputCaptureRejectsRebuiltInputs(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	binding, artifact := inputCaptureAccount(t)
	for _, compiled := range []bool{false, true} {
		mode := "source"
		if compiled {
			mode = "compiled"
		}
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "run.jsonl")
			inputCaptureWriteSteps(t, path, false, inputCaptureStep(1, "deposit", 5, 5))
			check := Check{Binding: binding, Files: []string{path}, Options: validate.Options{Dir: t.TempDir(), NoITF: true}}
			if compiled {
				check.Compiled = artifact
			}
			j, err := check.Judge(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			original := j.Inputs[0]
			j.Inputs[0] = Input{Path: original.Path, Format: original.Format, Steps: original.Steps}
			m, err := j.Manifest()
			if err == nil || m != nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "Check.Judge") {
				t.Fatalf("rebuilt input bypassed capture provenance: manifest=%+v error=%v", m, err)
			}
			j.Inputs[0] = original
			inputCaptureManifest(t, j)
		})
	}
}
