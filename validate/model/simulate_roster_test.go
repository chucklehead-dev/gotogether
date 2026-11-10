package model

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rosterSource is a fake quint whose verification of invariant slow
// sleeps for seconds, of slower for a minute, and of every other invariant
// proves at once, with bindings slow.yaml (holds [slow]), fast<i>.yaml
// (holds [fast]) and, with slower, slower.yaml last (holds [slower]).
func rosterSource(t *testing.T, seconds string, fast int, slower ...bool) (string, []string) {
	t.Helper()
	script := "case \" $* \" in\n *\" --invariant slow \"*)\n sleep " + seconds + "\n" + nativeFakeScript(nativeProofJSON, "", 0, "slow proof") + ";;\n" +
		" *\" --invariant slower \"*)\n sleep 60\n" + nativeFakeScript(nativeProofJSON, "", 0, "slower proof") + ";;\n" +
		" *)\n" + nativeFakeScript(nativeProofJSON, "", 0, "proof") + ";;\nesac\n"
	source := comparisonFakeSource(t, script)
	dir := filepath.Dir(source.checker.Binding.Spec)
	if err := os.WriteFile(source.checker.Binding.Spec, []byte("module source { var x: int\naction init = x' = 0\naction step = x' = x\nval slow = true\nval slower = true\nval fast = true\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	write := func(name, inv string) string {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte("spec: source.qnt\nmodule: source\ninvariants: ["+inv+"]\nsimulate: {holds: ["+inv+"], verifySteps: 2}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	paths := []string{write("slow", "slow")}
	for i := range fast {
		paths = append(paths, write("fast"+string(rune('1'+i)), "fast"))
	}
	if len(slower) > 0 && slower[0] {
		paths = append(paths, write("slower", "slower"))
	}
	return source.checker.Options.Quint, paths
}

// resultBinding is the binding a SimulateMany result names.
func resultBinding(t *testing.T, report *Report, err error) string {
	t.Helper()
	if be, ok := errors.AsType[*BindingError](err); ok && report == nil {
		return be.Binding
	}
	if report == nil || err != nil {
		t.Fatalf("a result names no binding: %+v %v", report, err)
	}
	return report.Binding
}

type rosterResult struct {
	binding string
	report  *Report
	err     error
}

func runRoster(t *testing.T, quint string, paths []string, within time.Duration) ([]rosterResult, time.Duration) {
	t.Helper()
	start := time.Now()
	var results []rosterResult
	opt := SimulateOptions{Quint: quint, Verify: 2, Deadline: start.Add(within)}
	for report, err := range simulateMany(t.Context(), paths, opt, stableTestTool, trustedTestBackend) {
		results = append(results, rosterResult{resultBinding(t, report, err), report, err})
	}
	return results, time.Since(start)
}

// A slow binding first must not leave the bindings after it unvisited:
// each gets its share of the deadline, the slow one runs out of its own
// and of the time the others leave, and is reported unfinished: no
// verdict, never a pass or a skip, while the others pass.
func TestSimulateManySlowFirstDoesNotStarveTheRest(t *testing.T) {
	quint, paths := rosterSource(t, "60", 2)
	results, elapsed := runRoster(t, quint, paths, 3*time.Second)
	if elapsed > 10*time.Second {
		t.Fatalf("the roster outlived its deadline: %s", elapsed)
	}
	if len(results) != len(paths) {
		t.Fatalf("every binding must report: got %d of %d: %+v", len(results), len(paths), results)
	}
	// The slow binding is retried after the others, so it reports last.
	for i, want := range []string{paths[1], paths[2], paths[0]} {
		if results[i].binding != want {
			t.Fatalf("report %d is %s, want %s", i, results[i].binding, want)
		}
	}
	for _, r := range results[:2] {
		if r.err != nil || r.report.Code != 0 || r.report.Unfinished != "" || len(r.report.Invariants) != 1 || !r.report.Invariants[0].Held {
			t.Fatalf("a fast binding after the slow one did not pass: %+v %v", r.report, r.err)
		}
	}
	slow := results[2]
	if slow.err != nil || slow.report.Code != 2 || slow.report.Skipped != "" || len(slow.report.Invariants) != 0 ||
		!strings.Contains(slow.report.Unfinished, "share of the deadline") || !strings.Contains(slow.report.Unfinished, "retried") {
		t.Fatalf("the slow binding is not reported unfinished: %+v %v", slow.report, slow.err)
	}
	doc := slow.report.Doc()
	if doc.Verdict != "no verdict" || doc.Exit != 2 || doc.Unfinished == "" || doc.SkipReason != "" || len(doc.Checks) != 0 {
		t.Fatalf("unfinished document: %+v", doc)
	}
}

// What the fast bindings leave goes to the one that ran out of its first
// share: a slow proof that fits the time left after them is retried and
// finishes with its verdict.
func TestSimulateManyRetryGetsTheTimeLeft(t *testing.T) {
	quint, paths := rosterSource(t, "3", 2)
	// First shares: 7.5s/3 = 2.5s for the slow one, which its 3s sleep
	// overruns; the fast ones take milliseconds; the retry has about 5s.
	results, _ := runRoster(t, quint, paths, 7500*time.Millisecond)
	if len(results) != 3 || results[2].binding != paths[0] {
		t.Fatalf("the slow binding was not retried last: %+v", results)
	}
	for _, r := range results {
		if r.err != nil || r.report.Code != 0 || r.report.Unfinished != "" {
			t.Fatalf("%s: %+v %v", r.binding, r.report, r.err)
		}
	}
}

// A slow binding late in the roster gets the same first share as the
// others, not all that the fast ones before it left: otherwise it spends
// it, runs out anyway, and the earlier slow binding's retry has nothing.
// With the same shares, the earlier one's retry gets what is left and
// finishes; only the one that cannot finish is unfinished.
func TestSimulateManyLateSlowBindingDoesNotTakeTheRetriesTime(t *testing.T) {
	quint, paths := rosterSource(t, "3", 4, true)
	// Six bindings in 12s: first shares of 2s; slow (3s) and slower (60s)
	// run out, the fast ones take milliseconds; slow's retry has about 4s.
	results, elapsed := runRoster(t, quint, paths, 12*time.Second)
	if elapsed > 20*time.Second || len(results) != 6 {
		t.Fatalf("%d results in %s", len(results), elapsed)
	}
	byPath := map[string]rosterResult{}
	for _, r := range results {
		byPath[r.binding] = r
	}
	if r := byPath[paths[0]]; r.err != nil || r.report.Code != 0 || r.report.Unfinished != "" {
		t.Fatalf("the early slow binding's retry was starved: %+v %v", r.report, r.err)
	}
	if r := byPath[paths[5]]; r.err != nil || r.report.Code != 2 || r.report.Unfinished == "" {
		t.Fatalf("the slower binding: %+v %v", r.report, r.err)
	}
}

// Without a deadline, and after the caller's cancellation, every binding
// still reports: a canceled roster names each remaining binding with the
// cancellation, starting no work, and never as unfinished.
func TestSimulateManyCanceledStillReportsEveryBinding(t *testing.T) {
	quint, paths := rosterSource(t, "60", 2)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var seen []string
	for report, err := range simulateMany(ctx, paths, SimulateOptions{Quint: quint, Verify: 2, Deadline: time.Now().Add(time.Hour)}, stableTestTool, trustedTestBackend) {
		if report != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled binding: %+v %v", report, err)
		}
		seen = append(seen, resultBinding(t, report, err))
	}
	if strings.Join(seen, ",") != strings.Join(paths, ",") {
		t.Fatalf("canceled roster lost bindings: %v", seen)
	}
}

// One binding's failure or error does not stop the roster: each later
// binding still reports its own verdict.
func TestSimulateManyContinuesAfterFailureAndError(t *testing.T) {
	quint, paths := rosterSource(t, "0", 1)
	missing := filepath.Join(filepath.Dir(paths[0]), "missing.yaml")
	var seen []string
	var reports []*Report
	for report, err := range simulateMany(t.Context(), []string{missing, paths[1]}, SimulateOptions{Quint: quint, Verify: 2}, stableTestTool, trustedTestBackend) {
		seen, reports = append(seen, resultBinding(t, report, err)), append(reports, report)
	}
	if len(seen) != 2 || seen[0] != missing || reports[0] != nil || seen[1] != paths[1] || reports[1].Code != 0 {
		t.Fatalf("roster stopped at an error: %v %+v", seen, reports)
	}
}

// A single Simulate with a deadline it cannot meet is unfinished, not an
// error and not a verdict.
func TestSimulateDeadlineUnfinished(t *testing.T) {
	quint, paths := rosterSource(t, "60", 0)
	report, err := Simulate(t.Context(), paths[0], SimulateOptions{Quint: quint, Verify: 2, Deadline: time.Now().Add(time.Second)})
	if err != nil || report == nil || report.Code != 2 || report.Unfinished == "" || strings.Contains(report.Unfinished, "retried") {
		t.Fatalf("%+v %v", report, err)
	}
}
