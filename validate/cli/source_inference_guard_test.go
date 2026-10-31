package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The real typecheck succeeds, then a deterministic wrapper changes a dependency
// before Go consumes its schema. Neither inferred data nor a different second
// binding load may silently become the judgment's identity.
func TestSourceInferenceBoundaries(t *testing.T) {
	skip.Quint(t)
	real, err := exec.LookPath("quint")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"spec", "binding"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			spec := filepath.Join(dir, "bignumber.qnt")
			sourceCaptureCopy(t, "../../itf/testdata/bignumber.qnt", spec)
			bp := filepath.Join(dir, "binding.yaml")
			sourceCaptureWrite(t, bp, []byte("spec: "+spec+"\nmodule: bignumber\nactions:\n  step: {}\n"))
			mutate := spec
			if target == "binding" {
				mutate = bp
			}
			wrapper := filepath.Join(dir, "quint")
			sourceCaptureWrite(t, wrapper, []byte("#!/bin/sh\n\"$GUARD_REAL_QUINT\" \"$@\"\nstatus=$?\nif [ \"$1\" = typecheck ] && [ ! -e \"$GUARD_MARK\" ]; then\n  touch \"$GUARD_MARK\"\n  printf '\\n// changed\\n' >> \"$GUARD_MUTATE\"\nfi\nexit \"$status\"\n"))
			if target == "binding" {
				data := sourceCaptureRead(t, wrapper)
				sourceCaptureWrite(t, wrapper, []byte(strings.Replace(string(data), "// changed", "# changed", 1)))
			}
			if err := os.Chmod(wrapper, 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GUARD_REAL_QUINT", real)
			t.Setenv("GUARD_MARK", filepath.Join(dir, "changed"))
			t.Setenv("GUARD_MUTATE", mutate)
			check := Check{Binding: bp, Options: validate.Options{Quint: wrapper, Dir: t.TempDir(), NoITF: true}}
			trace := "../../itf/testdata/bignumber.rust.itf.json"
			if target == "spec" {
				b, err := binding.Load(bp)
				if err != nil {
					t.Fatal(err)
				}
				_, original := input.Read(trace, "")
				a, ok := errors.AsType[*itf.AmbiguousError](original)
				if !ok {
					t.Fatalf("expected ambiguous trace: %v", original)
				}
				_, _, err = check.readITFByTypes(trace, b, a, original)
				if err == nil || !strings.Contains(err.Error(), "changed") {
					t.Fatalf("source edit during inference accepted: %v", err)
				}
			} else {
				log := filepath.Join(dir, "steps.jsonl")
				inputCaptureWriteSteps(t, log, false, inputCaptureStep(1, "deposit", 5, 5))
				check.Files = []string{trace, log}
				j, err := check.Judge(context.Background())
				if j != nil {
					defer j.Close()
				}
				if err == nil || !strings.Contains(err.Error(), "changed while inputs were read") {
					t.Fatalf("split binding load accepted: %v", err)
				}
			}
		})
	}
}
