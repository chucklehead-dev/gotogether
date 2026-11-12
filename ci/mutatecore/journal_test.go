package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testJournal(t *testing.T) *journal {
	t.Helper()
	j, err := openJournal(t.TempDir(), "qenv", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.file.Close() })
	return j
}
func journalRecords(t *testing.T, path string) []journalEvent {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var result []journalEvent
	dec := json.NewDecoder(file)
	for {
		var e journalEvent
		err := dec.Decode(&e)
		if errors.Is(err, io.EOF) {
			return result
		}
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, e)
	}
}
func syntheticCandidates() []candidate {
	return []candidate{
		{mutant: mutant{source: source{file: "qenv/a.go", ordinal: 0}, descriptor: "a.go:1 f: drop call (a())"}, targets: []target{{1, 35}, {0, 104}}},
		{mutant: mutant{source: source{file: "qenv/a.go", ordinal: 1}, descriptor: "a.go:2 f: drop call (b())"}, targets: []target{{0, 105}}},
	}
}

func TestJournalCapturesSourceIDsActualAttemptsAndRedComplete(t *testing.T) {
	j := testJournal(t)
	c := syntheticCandidates()
	results, err := runCampaign(t.Context(), c, 1, unsharded(0), func(_ context.Context, t target) (executionResult, error) {
		if t.id == 35 {
			return exited(1), nil
		}
		return exited(0), nil
	}, j, func(int) {})
	if err != nil || len(results) != 2 || results[0] || !results[1] {
		t.Fatalf("results=%v err=%v", results, err)
	}
	events := journalRecords(t, j.path)
	if len(events) != 3 || events[0].Event != "outcome" || events[1].Event != "outcome" || events[2].Event != "complete" {
		t.Fatalf("events=%v", events)
	}
	raw, _ := json.Marshal(events[0].Data)
	var first outcomeRecord
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatal(err)
	}
	if first.Outcome != "killed" || len(first.Source.Targets) != 2 || len(first.Attempts) != 1 || first.Attempts[0].ID != 35 || first.Attempts[0].Suite != 1 {
		t.Fatalf("wrong identity or actual executions: %+v", first)
	}
	raw, _ = json.Marshal(events[2].Data)
	var complete completeRecord
	if err := json.Unmarshal(raw, &complete); err != nil {
		t.Fatal(err)
	}
	if complete.Total != 2 || complete.Survivors != 1 || complete.Gate != gateFailed {
		t.Fatalf("red gate manufactured green: %+v", complete)
	}
}

func TestCampaignErrorsNeverBecomeKillsOrComplete(t *testing.T) {
	j := testJournal(t)
	result, err := runCampaign(t.Context(), syntheticCandidates(), 1, unsharded(0), func(context.Context, target) (executionResult, error) {
		return executionResult{Disposition: "execution_error"}, errors.New("fixture execution failed")
	}, j, func(int) { t.Fatal("error counted as completion") })
	if err == nil || result != nil {
		t.Fatalf("result=%v err=%v", result, err)
	}
	events := journalRecords(t, j.path)
	if len(events) != 1 || events[0].Event != "outcome" {
		t.Fatalf("unexpected terminal event: %v", events)
	}
	raw, _ := json.Marshal(events[0].Data)
	var record outcomeRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record.Outcome != "error" || record.Attempts[0].Outcome != "error" {
		t.Fatalf("error classified as verdict: %+v", record)
	}
}

func TestJournalWriteAndSyncFailuresFailClosed(t *testing.T) {
	t.Run("write", func(t *testing.T) {
		j := testJournal(t)
		j.file.Close()
		results, err := runCampaign(t.Context(), syntheticCandidates(), 1, unsharded(0), func(context.Context, target) (executionResult, error) { return exited(0), nil }, j, func(int) { t.Fatal("failed record counted") })
		if err == nil || results != nil {
			t.Fatalf("write failure returned success: %v %v", results, err)
		}
	})
	t.Run("sync", func(t *testing.T) {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		defer writer.Close()
		j := &journal{file: writer}
		// One small record fits in the pipe; Sync on a pipe must fail.
		if err := j.append("outcome", map[string]string{"outcome": "survived"}); err == nil || !strings.Contains(err.Error(), "sync campaign record") {
			t.Fatalf("sync failure ignored: %v", err)
		}
	})
}

func TestJournalPathsRetainEarlierRuns(t *testing.T) {
	root := t.TempDir()
	a, err := openJournal(root, "internal/depgraph", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.file.Close()
	b, err := openJournal(root, "internal/depgraph", "")
	if err != nil {
		t.Fatal(err)
	}
	defer b.file.Close()
	if a.path == b.path || filepath.Dir(a.path) != filepath.Join(root, ".gotogether", "mutations") {
		t.Fatalf("unsafe default paths: %q %q", a.path, b.path)
	}
	explicit := "records/campaign.jsonl"
	c, err := openJournal(root, "qenv", explicit)
	if err != nil {
		t.Fatal(err)
	}
	defer c.file.Close()
	if _, err := openJournal(root, "qenv", explicit); err == nil {
		t.Fatal("overwrote existing evidence")
	}
}

// The subprocess takes a real termination signal after its first synced result. Its
// unfinished second source must remain unknown, without a complete summary.
func TestJournalCancellationProcess(t *testing.T) {
	if path := os.Getenv("MUTATECORE_CANCEL_CHILD"); path != "" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer stop()
		j, err := openJournal(filepath.Dir(path), "synthetic", path)
		if err != nil {
			panic(err)
		}
		candidates := syntheticCandidates()
		if err := j.append("admitted", []sourceRecord{sourceMetadata(candidates[0]), sourceMetadata(candidates[1])}); err != nil {
			panic(err)
		}
		_, err = runCampaign(ctx, candidates, 1, unsharded(0), func(ctx context.Context, t target) (executionResult, error) {
			if t.id == 35 {
				return exited(1), nil
			}
			<-ctx.Done()
			return executionResult{Disposition: "canceled"}, context.Cause(ctx)
		}, j, func(n int) {
			if n == 1 {
				fmt.Println("first result synced")
			}
		})
		if err != nil {
			if logErr := j.append("aborted", map[string]string{"error": err.Error()}); logErr != nil {
				panic(logErr)
			}
			j.file.Close()
			os.Exit(1)
		}
		fmt.Println("complete summary")
		j.file.Close()
		os.Exit(0)
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		t.Run(sig.String(), func(t *testing.T) { assertJournalCancellation(t, sig) })
	}
}

func assertJournalCancellation(t *testing.T, sig syscall.Signal) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cancelled.jsonl")
	cmd := exec.CommandContext(ctx, executable, "-test.run", "^TestJournalCancellationProcess$")
	cmd.Env = append(os.Environ(), "MUTATECORE_CANCEL_CHILD="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil || line != "first result synced\n" {
		t.Fatalf("read readiness %q: %v", line, err)
	}
	// The file is readable before signaling; the completed source is durable.
	before := journalRecords(t, path)
	if len(before) < 2 || before[1].Event != "outcome" {
		t.Fatalf("first result not visible: %v", before)
	}
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	rest, readErr := io.ReadAll(reader)
	if readErr != nil {
		t.Fatal(readErr)
	}
	err = cmd.Wait()
	exit, ok := errors.AsType[*exec.ExitError](err)
	if !ok || sig == syscall.SIGTERM && exit.ExitCode() != 1 || sig == syscall.SIGKILL && exit.ExitCode() != -1 {
		t.Fatalf("interrupted campaign exit: %v", err)
	}
	if strings.Contains(string(rest), "complete summary") {
		t.Fatal("interruption printed complete summary")
	}
	events := journalRecords(t, path)
	for _, event := range events {
		if event.Event == "complete" {
			t.Fatal("interruption recorded completion")
		}
	}
	if sig == syscall.SIGTERM && events[len(events)-1].Event != "aborted" {
		t.Fatalf("missing abort evidence: %v", events)
	}
	// At most the first source has a verdict. An interrupted second source can
	// have an error record, but it can never be called killed or survived.
	verdicts := 0
	for _, event := range events {
		if event.Event == "outcome" {
			raw, _ := json.Marshal(event.Data)
			var record outcomeRecord
			if err := json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			if record.Outcome == "killed" || record.Outcome == "survived" {
				verdicts++
			}
		}
	}
	if verdicts != 1 {
		t.Fatalf("interruption manufactured verdicts: %d", verdicts)
	}
}

// Both the outer command and a spawned child share the owned process group.
// A private FIFO provides readiness without waiting on a timing assumption.
func TestOwnedCommandCancellationStopsSpawnedChild(t *testing.T) {
	parent, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
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
	stopClose := context.AfterFunc(parent, func() { ready.Close() })
	defer stopClose()
	// The child owns the pipe write end. It stays open only until the child
	// actually dies, so EOF proves cancellation reached past the outer shell.
	script := fmt.Sprintf("sleep 300 & child=$!; printf '%%s\\n' \"$child\" > %q; wait \"$child\"", fifo)
	cmd := groupCommand(ctx, "sh", "-c", script)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
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
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	stopRead := context.AfterFunc(parent, func() { stdout.Close() })
	defer stopRead()
	reason := errors.New("cancel fixture build")
	cancel(reason)
	// ReadAll cannot finish while an escaped descendant still holds stdout.
	if _, err := io.ReadAll(stdout); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("cancelled child-spawning command passed")
	}
	if parent.Err() != nil {
		t.Fatal("cancellation failed to stop descendants promptly")
	}
}

func TestExecuteCancellationIsNotKilledMutation(t *testing.T) {
	parent, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
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
	stopClose := context.AfterFunc(parent, func() { ready.Close() })
	defer stopClose()
	binary := filepath.Join(dir, "blocking-suite")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$$\" > %q\nexec sleep 300\n", fifo)
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	type execution struct {
		result executionResult
		err    error
	}
	result := make(chan execution, 1)
	go func() {
		observed, err := execute(ctx, suite{dir: dir, binary: binary}, "0", 3072*1024, mutantMode)
		result <- execution{observed, err}
	}()
	line, err := bufio.NewReader(ready).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer process.Release()
	defer process.Kill()
	reason := errors.New("cancel fixture mutation")
	cancel(reason)
	var got execution
	select {
	case got = <-result:
	case <-parent.Done():
		t.Fatal("cancelled execution did not stop")
	}
	if got.result.Disposition != "canceled" || got.result.ExitCode != nil || got.result.Signal != "" || got.result.SignalNumber != 0 || !errors.Is(got.err, reason) {
		t.Fatalf("cancelled mutation became a verdict: result=%+v err=%v", got.result, got.err)
	}
	assertJournalExecution(t, got.result, got.err)
}

func TestJournalMetadataPreservesInventoryAndSourceMappings(t *testing.T) {
	j := testJournal(t)
	suites := []suite{
		{pkg: coreModule + "/qenv", dir: "/root/qenv", binary: "/work/0/test", mutants: mustInventory(t, "qmut: mutant 0 qenv/root.go:1 f: drop call (f())\nqmut: mutant 1 qenv/internal/choice/choice.go:258 checked.of: drop call (r.check())\n")},
		{pkg: coreModule + "/qenv/internal/choice", dir: "/root/qenv/internal/choice", binary: "/work/1/test", mutants: mustInventory(t, "qmut: mutant 0 qenv/internal/choice/choice.go:258 checked.of: drop call (r.check())\n")},
	}
	for i, s := range suites {
		if err := j.append("suite", suiteMetadata(i, s)); err != nil {
			t.Fatal(err)
		}
	}
	candidates, err := admit(suites, func(s suite) error { return j.append("baseline", map[string]any{"package": s.pkg, "passed": true}) })
	if err != nil {
		t.Fatal(err)
	}
	var sources []sourceRecord
	for _, c := range candidates {
		sources = append(sources, sourceMetadata(c))
	}
	if err := j.append("admitted", sources); err != nil {
		t.Fatal(err)
	}
	events := journalRecords(t, j.path)
	if len(events) != 5 {
		t.Fatalf("lost metadata events: %v", events)
	}
	raw, _ := json.Marshal(events[0].Data)
	var root suiteRecord
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if root.Index != 0 || root.Package != suites[0].pkg || root.Directory != suites[0].dir || root.Binary != suites[0].binary || len(root.Inventory) != 2 || root.Inventory[1].ID != 1 || root.Inventory[1].File != "qenv/internal/choice/choice.go" {
		t.Fatalf("lost suite inventory: %+v", root)
	}
	raw, _ = json.Marshal(events[4].Data)
	var admitted []sourceRecord
	if err := json.Unmarshal(raw, &admitted); err != nil {
		t.Fatal(err)
	}
	if len(admitted) != 2 || admitted[0].File != "qenv/internal/choice/choice.go" || admitted[0].Ordinal != 0 || admitted[0].Descriptor == "" || len(admitted[0].Targets) != 2 || admitted[0].Targets[0] != (targetRecord{1, 0}) || admitted[0].Targets[1] != (targetRecord{0, 1}) {
		t.Fatalf("lost source/local-ID mapping: %+v", admitted)
	}
}
