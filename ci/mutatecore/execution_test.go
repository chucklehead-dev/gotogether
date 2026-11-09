package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Real tiny programs exercise the wrapper, not a guessed assertion classifier.
func TestExecutionStatusesAndJournalPropagation(t *testing.T) {
	for _, code := range []int{0, 1, 124, 125, 126, 127, 143, 200} {
		t.Run(exited(code).String(), func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "suite")
			script := "#!/bin/sh\nexit " + strconv.Itoa(code) + "\n"
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			j := testJournal(t)
			candidates := []candidate{{targets: []target{{0, 0}}}}
			results, err := runCampaign(t.Context(), candidates, 1, 0, func(ctx context.Context, _ target) (executionResult, error) {
				return execute(ctx, suite{dir: dir, binary: binary}, "0", 3072*1024, mutantMode)
			}, j, func(int) {})
			setup := code >= 125 && code <= 127
			if (err != nil) != setup || (!setup && (len(results) != 1 || results[0] != (code == 0))) {
				t.Fatalf("results=%v err=%v", results, err)
			}
			events := journalRecords(t, j.path)
			data, _ := json.Marshal(events[0].Data)
			var record outcomeRecord
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			attempt := record.Attempts[0]
			if attempt.Execution.ExitCode == nil || *attempt.Execution.ExitCode != code || attempt.Execution.Disposition != exited(code).Disposition || attempt.Execution.Signal != "" || attempt.Execution.SignalNumber != 0 {
				t.Fatalf("lost observed status: %+v", attempt)
			}
			if setup && (record.Outcome != "error" || len(events) != 1) {
				t.Fatalf("setup manufactured kill: %s", data)
			}
			if code == 1 && attempt.Execution.Disposition != "exited" {
				t.Fatal("exit 1 classified as behavioral evidence")
			}
		})
	}
}

func TestExecutionActualSignalTimeoutStartAndCancellation(t *testing.T) {
	t.Run("signal", func(t *testing.T) {
		r, err := runExecution(t.Context(), groupCommand(t.Context(), "sh", "-c", "kill -TERM $$"), false)
		if err != nil || r.Disposition != "signaled" || r.Signal != "terminated" || r.SignalNumber != 15 || r.ExitCode != nil {
			t.Fatalf("result=%+v err=%v", r, err)
		}
		assertJournalExecution(t, r, err)
	})
	t.Run("timeout", func(t *testing.T) {
		// Exercise GNU timeout's real deadline with a short bound. The production
		// command retains 300 seconds; no campaign or slow sleep is needed here.
		r, err := runExecution(t.Context(), groupCommand(t.Context(), "timeout", "0.02", "sh", "-c", "exec sleep 30"), false)
		if err != nil || r.Disposition != "timeout_status" || r.ExitCode == nil || *r.ExitCode != 124 {
			t.Fatalf("result=%+v err=%v", r, err)
		}
		assertJournalExecution(t, r, err)
	})
	t.Run("start", func(t *testing.T) {
		r, err := runExecution(t.Context(), groupCommand(t.Context(), filepath.Join(t.TempDir(), "absent")), false)
		if err == nil || r.Disposition != "execution_error" || r.ExitCode != nil || r.Signal != "" {
			t.Fatalf("result=%+v err=%v", r, err)
		}
		assertJournalExecution(t, r, err)
	})
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		r, err := runExecution(ctx, groupCommand(ctx, "sh", "-c", "exit 0"), false)
		if !errors.Is(err, context.Canceled) || r.Disposition != "canceled" || r.ExitCode != nil || r.Signal != "" {
			t.Fatalf("result=%+v err=%v", r, err)
		}
		assertJournalExecution(t, r, err)
	})
}

func assertJournalExecution(t *testing.T, result executionResult, executionErr error) {
	t.Helper()
	j := testJournal(t)
	_, err := runCampaign(t.Context(), []candidate{{targets: []target{{0, 0}}}}, 1, 0, func(context.Context, target) (executionResult, error) { return result, executionErr }, j, func(int) {})
	if (err != nil) != (executionErr != nil) {
		t.Fatalf("campaign changed execution error: %v", err)
	}
	events := journalRecords(t, j.path)
	data, _ := json.Marshal(events[0].Data)
	var record outcomeRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	actual := record.Attempts[0].Execution
	expectedJSON, _ := json.Marshal(result)
	actualJSON, _ := json.Marshal(actual)
	if string(expectedJSON) != string(actualJSON) {
		t.Fatalf("execution metadata changed: %s -> %s", expectedJSON, actualJSON)
	}
	if executionErr != nil && (record.Outcome != "error" || len(events) != 1) {
		t.Fatalf("error became kill/complete: %s", data)
	}
}

func TestCampaignRejectsUnresolvedExecutionEvidence(t *testing.T) {
	code := 0
	for _, r := range []executionResult{{}, {Disposition: "canceled"}, {Disposition: "execution_error"}, exited(125), {Disposition: "timeout_status", ExitCode: &code}, {Disposition: "exited", ExitCode: &code, Signal: "killed"}} {
		j := testJournal(t)
		_, err := runCampaign(t.Context(), []candidate{{targets: []target{{0, 0}}}}, 1, 0, func(context.Context, target) (executionResult, error) { return r, nil }, j, func(int) { t.Fatal("unresolved attempt completed") })
		if err == nil {
			t.Fatalf("accepted %+v", r)
		}
		events := journalRecords(t, j.path)
		if len(events) != 1 {
			t.Fatalf("manufactured completion: %+v", events)
		}
	}
}
