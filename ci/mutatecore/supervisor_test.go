package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the real executable supervisor, its independently waited worker and
// durable sidecars with an owned tiny generator. No repository mutation suite,
// network dependency or native verifier is involved.
func TestSupervisorSealsCompleteRedCampaignAndRejectsIncompleteWorker(t *testing.T) {
	if testing.Short() {
		t.Skip("builds executable supervisor and tiny owned generator")
	}
	helper := filepath.Join(t.TempDir(), "mutatecore")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", helper, ".")
	build.Env = supervisorFixtureEnv(nil)
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build supervisor: %v\n%s", err, b)
	}
	for _, tc := range []struct {
		name, mode, flags string
		sealed, rechecked bool
	}{
		{name: "complete red", sealed: true, rechecked: true},
		{name: "unsupported provenance preserves count gate", flags: "-mod=mod", sealed: true, rechecked: false},
		{name: "generator failure", mode: "fail"},
		{name: "source drift", mode: "drift"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := supervisorFixture(t)
			log := filepath.Join(root, ".gotogether", "mutations", "fixture.jsonl")
			if err := os.MkdirAll(filepath.Dir(log), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), helper, root, "sample", "0")
			cmd.Env = supervisorFixtureEnv(map[string]string{"FIXTURE_GENERATOR_MODE": tc.mode, "GOFLAGS": tc.flags, "GOTOGETHER_MUTATION_LOG": log})
			output, err := cmd.CombinedOutput()
			exit, ok := errors.AsType[*exec.ExitError](err)
			if !ok || exit.ExitCode() != 1 {
				t.Fatalf("external gate should be red: %v\n%s", err, output)
			}
			receiptPath := log + ".receipt.json"
			if !tc.sealed {
				if _, err := os.Stat(receiptPath); !os.IsNotExist(err) {
					t.Fatalf("incomplete/drifted worker has receipt: %v\n%s", err, output)
				}
				if tc.mode == "fail" {
					b, err := os.ReadFile(log)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(string(b), `"event":"aborted"`) || strings.Contains(string(b), `"event":"complete"`) {
						t.Fatalf("failed worker journal: %s", b)
					}
				} else if !strings.Contains(string(output), "source/build inputs or discovery changed") {
					t.Fatalf("source drift failed for unrelated reason:\n%s", output)
				}
				return
			}
			var receipt completionReceipt
			if err := readJSONFile(receiptPath, &receipt); err != nil {
				t.Fatalf("missing valid sealed receipt: %v\n%s", err, output)
			}
			if receipt.Complete != (completeRecord{Total: 1, Survivors: 1, Gate: gateFailed}) || !receipt.WorkerExecution.passed() || receipt.AdmissionEligible {
				t.Fatalf("complete red campaign was reclassified: %+v", receipt)
			}
			if receipt.InputsRechecked != tc.rechecked {
				t.Fatalf("inputs rechecked=%v want %v", receipt.InputsRechecked, tc.rechecked)
			}
			if tc.rechecked && receipt.Inputs.Unavailable != "" {
				t.Fatalf("unexpected unavailable inputs: %s", receipt.Inputs.Unavailable)
			}
			if !tc.rechecked && receipt.Inputs.Unavailable == "" {
				t.Fatal("unsupported provenance silently claimed accounted")
			}
			if !strings.Contains(string(output), "1 survivors exceed limit 0") {
				t.Fatalf("complete red lost count-gate diagnostic:\n%s", output)
			}
			digest, size, err := digestFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Journal.SHA256 != digest || receipt.Journal.Size != size {
				t.Fatal("receipt journal digest/length does not match actual closed journal")
			}
			sidecar, err := os.ReadFile(log + ".inputs.json")
			if err != nil {
				t.Fatal(err)
			}
			var inputs provenanceContext
			if err := json.Unmarshal(sidecar, &inputs); err != nil {
				t.Fatal(err)
			}
			inputDigest, err := valueDigest(inputs)
			if err != nil || inputDigest != receipt.InputDigest {
				t.Fatalf("input sidecar identity mismatch: %v", err)
			}
		})
	}
}

func supervisorFixtureEnv(extra map[string]string) []string {
	values := map[string]string{"GOWORK": "off", "CGO_ENABLED": "0", "GOFLAGS": "", "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "JOBS": "1", "MEM_MB": "3072", "GOTOGETHER_MUTANT": "", "GOTOGETHER_MUTATION_LOG": "", "FIXTURE_GENERATOR_MODE": ""}
	for k, v := range extra {
		values[k] = v
	}
	var result []string
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if _, ok := values[k]; !ok {
			result = append(result, e)
		}
	}
	for k, v := range values {
		result = append(result, k+"="+v)
	}
	return result
}
func supervisorFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                "module github.com/chucklehead-dev/gotogether\n\ngo 1.27.3\n",
		"sample/source.go":      "package sample\nfunc f() {}\n",
		"sample/source_test.go": "package sample\nimport \"testing\"\nfunc TestFixture(t *testing.T) {}\n",
		"qweave/go.mod":         "module fixture/qweave\n\ngo 1.27.3\n",
		"qweave/cmd/qweave/main.go": `package main
import("os";"path/filepath")
func main(){
 if os.Getenv("FIXTURE_GENERATOR_MODE")=="fail"{os.Exit(1)}
 var out,root string
 for i,a:=range os.Args {if i+1<len(os.Args){if a=="-o"{out=os.Args[i+1]};if a=="-C"{root=os.Args[i+1]}}}
 if out==""{panic("missing output binary")}
 script:="#!/bin/sh\nif [ \"$GOTOGETHER_MUTANT\" = list ]; then\n printf '%s\\n' 'qmut: mutant 0 sample/source.go:1 f: drop call (f())'\nfi\nexit 0\n"
 if err:=os.WriteFile(out,[]byte(script),0700);err!=nil{panic(err)}
 if os.Getenv("FIXTURE_GENERATOR_MODE")=="drift"{p:=filepath.Join(root,"sample","source.go");f,err:=os.OpenFile(p,os.O_APPEND|os.O_WRONLY,0);if err!=nil{panic(err)};if _,err=f.WriteString("// changed while worker built\n");err!=nil{panic(err)};if err=f.Close();err!=nil{panic(err)}}
}
`,
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
