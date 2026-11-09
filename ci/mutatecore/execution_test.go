package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Real tiny programs exercise the wrapper, not a guessed assertion classifier.
func TestExecutionStatusesAndJournalPropagation(t *testing.T) {
	for _, tc := range []struct {
		code        int
		disposition string
	}{{0, "exited"}, {1, "exited"}, {124, "timeout_status"}, {125, "setup_status"}, {126, "setup_status"}, {127, "setup_status"}, {143, "signal_status"}, {200, "exited"}} {
		code := tc.code
		t.Run(strconv.Itoa(code), func(t *testing.T) {
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
			if attempt.Execution.ExitCode == nil || *attempt.Execution.ExitCode != code || attempt.Execution.Disposition != tc.disposition || attempt.Execution.Signal != "" || attempt.Execution.SignalNumber != 0 {
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
	for _, r := range []executionResult{{}, {Disposition: "canceled"}, {Disposition: "execution_error"}, exited(125), {Disposition: "timeout_status", ExitCode: &code}, {Disposition: "exited", ExitCode: &code, Signal: "killed"}, {Disposition: "signaled", SignalNumber: 15, Signal: "killed"}, {Disposition: "signaled", SignalNumber: 65, Signal: syscall.Signal(65).String()}, {Disposition: "signaled", SignalNumber: -1, Signal: syscall.Signal(-1).String()}} {
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

// A TERM-ignoring test must be stopped by the owned deadline, rather than the
// outer cancellation safety net. Readiness yields a live process handle before
// waiting, so cleanup cannot target a PID reused after the fixture exits.
func TestExecutionDeadlineStopsTermIgnoringTest(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	dir := t.TempDir()
	fifo := filepath.Join(dir, "ready")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	ready, err := os.OpenFile(fifo, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer ready.Close()
	stopRead := context.AfterFunc(ctx, func() { ready.Close() })
	defer stopRead()
	binary := filepath.Join(dir, "suite")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\ntrap '' TERM\nprintf '%s\\n' \"$$\" > \"$MUTATECORE_READY\"\necho ready\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := executionCommand(ctx, suite{dir: dir, binary: binary}, "0", 3072*1024, mutantMode, "0.2")
	cmd.Env = append(cmd.Env, "MUTATECORE_READY="+fifo)
	type observation struct {
		result executionResult
		err    error
	}
	done := make(chan observation, 1)
	started := time.Now()
	go func() { r, err := runExecution(ctx, cmd, true); done <- observation{r, err} }()
	line, err := bufio.NewReader(ready).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	child, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Release()
	defer child.Kill()
	var got observation
	select {
	case got = <-done:
	case <-ctx.Done():
		t.Fatal("owned deadline failed; outer cancellation was required")
	}
	if ctx.Err() != nil {
		t.Fatal("owned deadline failed; outer cancellation was required")
	}
	result := got.result
	// GNU timeout versions may report either their own SIGKILL or exit 124.
	// Neither observation alone proves termination: check the owned child below.
	killed := result.Disposition == "signaled" && result.SignalNumber == 9 && result.ExitCode == nil
	timedOut := result.Disposition == "timeout_status" && result.ExitCode != nil && *result.ExitCode == 124
	if got.err != nil || !killed && !timedOut {
		t.Fatalf("result=%+v error=%v", result, got.err)
	}
	if !strings.Contains(result.Output, "ready") {
		t.Fatal("TERM-ignoring fixture did not start")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("deadline did not stop promptly")
	}
	if !deadlineChildStopped(child) {
		t.Fatal("timeout returned but TERM-ignoring child is still running")
	}
	assertJournalExecution(t, result, got.err)
}

// A zombie is dead but may await reaping by the container's init. Signal uses
// the process handle; procfs is read-only and never used to select a kill target.
func deadlineChildStopped(child *os.Process) bool {
	if err := child.Signal(syscall.Signal(0)); errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", child.Pid))
	return err == nil && strings.Contains(string(stat), ") Z ")
}

func TestDeadlineChildStoppedRejectsLiveProcess(t *testing.T) {
	cmd := groupCommand(t.Context(), "sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	if deadlineChildStopped(cmd.Process) {
		t.Fatal("live process was accepted as stopped")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if !deadlineChildStopped(cmd.Process) {
		t.Fatal("reaped process was reported running")
	}
}
