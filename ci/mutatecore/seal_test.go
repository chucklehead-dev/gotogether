package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sealedFixture(t *testing.T) (string, producerManifest) {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "test-binary")
	if err := os.WriteFile(binary, []byte("fixture binary identity"), 0600); err != nil {
		t.Fatal(err)
	}
	s := suite{pkg: coreModule + "/qenv", dir: dir, binary: binary, mutants: []mutant{{source: source{"qenv/a.go", 0}, descriptor: "a.go:1 f: negate if (x)", id: 0}}}
	sr := suiteMetadata(0, s)
	hash, size, err := digestFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	start, _ := json.Marshal(map[string]any{"version": 1, "root": dir, "package": "qenv", "jobs": 1, "mem_mb": 3072, "timeout_seconds": 300, "max_survivors": 0, "input_digest": digest})
	manifest := producerManifest{InputDigest: digest, Start: start, Suites: []suiteRecord{sr}, Binaries: []binaryIdentity{{Suite: 0, SHA256: hash, Size: size}}}
	j, err := openJournal(dir, "qenv", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := j.append("start", json.RawMessage(start)); err != nil {
		t.Fatal(err)
	}
	if err := j.append("suite", sr); err != nil {
		t.Fatal(err)
	}
	if err := j.append("baseline", sealedBaseline{Package: s.pkg, Passed: true, Execution: exited(0)}); err != nil {
		t.Fatal(err)
	}
	candidates, err := admit([]suite{s}, func(suite) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := j.append("admitted", sealedAdmitted{Sources: []sourceRecord{sourceMetadata(candidates[0])}, Suites: 1}); err != nil {
		t.Fatal(err)
	}
	_, err = runCampaign(t.Context(), candidates, 1, 0, func(context.Context, target) (executionResult, error) { return exited(0), nil }, j, func(int) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := j.file.Close(); err != nil {
		t.Fatal(err)
	}
	return j.path, manifest
}

func TestValidateJournalPreservesRedComplete(t *testing.T) {
	path, m := sealedFixture(t)
	c, err := validateJournal(path, m)
	if err != nil || c.Total != 1 || c.Survivors != 1 || c.GatePassed {
		t.Fatalf("complete=%+v error=%v", c, err)
	}
}

func TestValidateJournalRejectsChangedBuildAndIndependentRoster(t *testing.T) {
	t.Run("binary", func(t *testing.T) {
		path, m := sealedFixture(t)
		if err := os.WriteFile(m.Suites[0].Binary, []byte("other build"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := validateJournal(path, m); err == nil {
			t.Fatal("changed binary sealed")
		}
	})
	t.Run("inventory", func(t *testing.T) {
		path, m := sealedFixture(t)
		m.Suites[0].Inventory[0].Descriptor = "other source site"
		if _, err := validateJournal(path, m); err == nil {
			t.Fatal("different independent inventory sealed")
		}
	})
	t.Run("settings", func(t *testing.T) {
		path, m := sealedFixture(t)
		m.Start = []byte(strings.Replace(string(m.Start), `"jobs":1`, `"jobs":2`, 1))
		if _, err := validateJournal(path, m); err == nil {
			t.Fatal("changed settings sealed")
		}
	})
}

func TestValidateJournalRejectsIncompleteOrContradictoryEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(string) string
	}{
		{"missing complete", func(s string) string { return s[:strings.LastIndex(strings.TrimSuffix(s, "\n"), "\n")+1] }},
		{"truncated", func(s string) string { return s[:len(s)-4] }},
		{"trailing event", func(s string) string { return s + strings.Split(s, "\n")[0] + "\n" }},
		{"duplicate field", func(s string) string {
			return strings.Replace(s, `"event":"start"`, `"event":"start","event":"start"`, 1)
		}},
		{"alias", func(s string) string { return strings.Replace(s, `"event":"start"`, `"Event":"start"`, 1) }},
		{"missing execution", func(s string) string {
			return strings.Replace(s, `"execution":{"disposition":"exited","exit_code":0},`, "", 1)
		}},
		{"contradictory status", func(s string) string { return strings.ReplaceAll(s, `"exit_code":0`, `"exit_code":1`) }},
		{"wrong complete total", func(s string) string { return strings.Replace(s, `"total":1`, `"total":2`, 1) }},
		{"absent mandatory bool", func(s string) string { return strings.Replace(s, `"passed":true,`, "", 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, m := sealedFixture(t)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.change(string(b))), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := validateJournal(path, m); err == nil {
				t.Fatal("invalid evidence sealed")
			}
		})
	}
}

func TestValidateOutcomeRequiresActualSuccessfulPrefix(t *testing.T) {
	targets := []targetRecord{{Suite: 0, ID: 1}, {Suite: 1, ID: 10}}
	valid := outcomeRecord{Source: sourceRecord{Targets: targets}, Outcome: "killed", Attempts: []attemptRecord{{targetRecord: targets[0], Outcome: "passed", Execution: exited(0)}, {targetRecord: targets[1], Outcome: "failed", Execution: exited(124)}}}
	if err := validateOutcome(valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		modify func(*outcomeRecord)
	}{
		{"skip first", func(o *outcomeRecord) { o.Attempts = o.Attempts[1:] }},
		{"false survival", func(o *outcomeRecord) { o.Outcome = "survived"; o.Attempts = o.Attempts[:1] }},
		{"attempt after kill", func(o *outcomeRecord) { o.Attempts[0].Outcome = "failed"; o.Attempts[0].Execution = exited(1) }},
		{"missing disposition", func(o *outcomeRecord) { o.Attempts[1].Execution = executionResult{} }},
		{"setup failure", func(o *outcomeRecord) { o.Attempts[1].Execution = exited(125) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := valid
			o.Attempts = append([]attemptRecord(nil), valid.Attempts...)
			tc.modify(&o)
			if err := validateOutcome(o); err == nil {
				t.Fatal("invalid attempt prefix accepted")
			}
		})
	}
}

func TestPublishJSONRefusesExistingReceipt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sealed.json")
	if err := publishJSON(path, map[string]bool{"complete": true}); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := publishJSON(path, map[string]bool{"complete": false}); err == nil {
		t.Fatal("receipt replaced")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(original) {
		t.Fatal("original receipt changed")
	}
	if err := publishJSON(filepath.Join(path, "child"), true); err == nil {
		t.Fatal("publication to invalid directory succeeded")
	}
}
