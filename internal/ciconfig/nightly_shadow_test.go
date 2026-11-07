package ciconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type nightlyWorkflow struct {
	Env  map[string]string `yaml:"env"`
	Jobs map[string]struct {
		Env      map[string]string `yaml:"env"`
		Strategy struct {
			Matrix struct {
				Include []struct{ Example, Seeds, Short string } `yaml:"include"`
			} `yaml:"matrix"`
		} `yaml:"strategy"`
		Steps []struct{ Name, Run string } `yaml:"steps"`
	} `yaml:"jobs"`
}

func readNightly(t *testing.T) nightlyWorkflow {
	t.Helper()
	b, err := os.ReadFile("../../.github/workflows/nightly.yml")
	if err != nil {
		t.Fatal(err)
	}
	var w nightlyWorkflow
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestNightlySampleScope(t *testing.T) {
	w := readNightly(t)
	for _, key := range []string{"GOTOGETHER_SEEDS", "GOTOGETHER_HEGEL_SEED"} {
		if _, ok := w.Env[key]; ok {
			t.Fatalf("campaign setting %s leaked globally", key)
		}
	}
	if w.Jobs["fresh-seeds"].Env["GOTOGETHER_SEEDS"] != "500" || w.Jobs["core-properties"].Env["GOTOGETHER_HEGEL_SEED"] != "random" {
		t.Fatal("nightly campaign widening lost")
	}
	var command string
	for _, s := range w.Jobs["evaluator-shadow"].Steps {
		if s.Name == "the core's searches, checked by both" {
			command = s.Run
		}
	}
	if command == "" {
		t.Fatal("core shadow step missing")
	}
	dir := t.TempDir()
	// Execute the actual workflow command with a deliberately inherited campaign
	// override. The stand-in Go command checks its environment, not source text.
	goPath := filepath.Join(dir, "go")
	if err := os.WriteFile(goPath, []byte("#!/bin/sh\n[ -z \"${GOTOGETHER_SEEDS+x}\" ] || exit 42\n[ \"$GOTOGETHER_EVAL\" = shadow ] || exit 43\n"), 0700); err != nil {
		t.Fatal(err)
	}
	run := func(command string) error {
		cmd := exec.Command("bash", "-e", "-c", command)
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "GOTOGETHER_SEEDS=500")
		return cmd.Run()
	}
	if err := run(command); err != nil {
		t.Fatalf("core shadow inherits sample override: %v", err)
	}
	mutant := strings.Replace(command, "env -u GOTOGETHER_SEEDS ", "", 1)
	if mutant == command {
		t.Fatal("sample isolation control missing")
	}
	if err := run(mutant); err == nil {
		t.Fatal("negative control accepted inherited500-seed override")
	}
}

func TestNightlyRecordedWebjobsShadowAndDisagreement(t *testing.T) {
	w := readNightly(t)
	found := false
	for _, row := range w.Jobs["evaluator-shadow"].Strategy.Matrix.Include {
		if row.Example == "webjobs" {
			found = true
			if row.Seeds != "own" || row.Short != "true" {
				t.Fatal("webjobs shadow must explicitly select recorded short tier")
			}
		} else if row.Short != "" {
			t.Fatalf("unrelated shadow row %s reduced to short tier", row.Example)
		}
	}
	if !found {
		t.Fatal("webjobs recorded tier missing")
	}
	var tests, summary string
	for _, s := range w.Jobs["evaluator-shadow"].Steps {
		switch s.Name {
		case "the tests, every judgement checked by both":
			tests = s.Run
		case "the runs compared":
			summary = s.Run
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\n[ \"$GOTOGETHER_EVAL\" = shadow ] || exit 42\n[ -z \"${GOTOGETHER_SEEDS+x}\" ] || exit 43\nfor arg; do [ \"$arg\" = -short ] && exit 0; done\nexit 44\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-e", "-c", tests)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "RUNNER_TEMP="+dir, "SEEDS=own", "SHORT=true", "RUN=", "SKIP=", "GOTOGETHER_SEEDS=500")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("recorded shadow command: %v\n%s", err, out)
	}
	// An explicit false matrix value must keep the full tier; nonempty is
	// not equivalent to true for this workflow option.
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\nfor arg; do [ \"$arg\" = -short ] && exit 44; done\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("bash", "-e", "-c", tests)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "RUNNER_TEMP="+dir, "SEEDS=own", "SHORT=false", "RUN=", "SKIP=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("explicit false selected short tier: %v\n%s", err, out)
	}
	for _, disagree := range []bool{false, true} {
		content := "agree native-control\n"
		if disagree {
			content += "DISAGREE native-negative-control\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "shadow.log"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", "-e", "-c", summary)
		cmd.Env = append(os.Environ(), "RUNNER_TEMP="+dir)
		out, err := cmd.CombinedOutput()
		if (err != nil) != disagree {
			t.Fatalf("disagree=%t err=%v\n%s", disagree, err, out)
		}
	}
}
