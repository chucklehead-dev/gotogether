package model

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func batchFakeSource(t *testing.T, batch string, scalar map[string]bool) *GraphResult {
	t.Helper()
	script := "printf '%s\\n' \"$*\" >> \"$0.invocations\"\ncase \" $* \" in\n *\" --invariants \"*)\n" + batch + ";;\n"
	for _, name := range []string{"a", "b", "allow", "unclassified"} {
		result, trace, exit := nativeProofJSON, "", 0
		if scalar[name] {
			result, trace, exit = nativeCexJSON, nativeCexITF, 1
		}
		script += " *\" --invariant " + name + " \"*)\n" + nativeFakeScript(result, trace, exit, "scalar "+name) + ";;\n"
	}
	script += " *)\n" + nativeFakeScript(nativeProofJSON, "", 0, "sample") + ";;\nesac\n"
	return comparisonFakeSource(t, script)
}

func batchBinding(t *testing.T, source *GraphResult, section string) string {
	t.Helper()
	text := "module source { var x: int\naction init = x' = 0\naction step = x' = x\nval a = true\nval b = true\nval allow = false\nval unclassified = true\n}\n"
	if err := os.WriteFile(source.checker.Binding.Spec, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(filepath.Dir(source.checker.Binding.Spec), "source.yaml")
	if err := os.WriteFile(file, []byte("spec: source.qnt\nmodule: source\ninvariants: [b, allow, a, unclassified]\nsimulate: "+section+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func batchInvocations(t *testing.T, source *GraphResult) [][]string {
	t.Helper()
	b, err := os.ReadFile(source.checker.Options.Quint + ".invocations")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		calls = append(calls, strings.Fields(line))
	}
	return calls
}

func TestNativeBatchPreservesMemberVerdicts(t *testing.T) {
	for _, failed := range []string{"", "a", "b", "ab"} {
		t.Run("violated-"+failed, func(t *testing.T) {
			scalar := map[string]bool{"a": strings.Contains(failed, "a"), "b": strings.Contains(failed, "b"), "allow": true}
			group := nativeFakeScript(nativeProofJSON, "", 0, "batch published")
			if failed != "" {
				group = nativeFakeScript(nativeCexJSON, nativeCexITF, 1, "batch published")
			}
			source := batchFakeSource(t, group, scalar)
			file := batchBinding(t, source, "{holds: [a, b], allows: [allow], verifySteps: 3}")
			var progress bytes.Buffer
			report, err := Simulate(t.Context(), file, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 8, Progress: &progress})
			if err != nil {
				t.Fatal(err)
			}
			if report.Code != map[bool]int{true: 1, false: 0}[failed != ""] {
				t.Fatalf("report code %d", report.Code)
			}
			names := []string{"b", "allow", "a", "unclassified"}
			if len(report.Invariants) != len(names) {
				t.Fatal(report.Invariants)
			}
			for i, v := range report.Invariants {
				if v.Name != names[i] || v.Violated != scalar[v.Name] || v.Held == scalar[v.Name] || v.Bound.Kind != "verified" || v.Bound.Steps != 3 {
					t.Fatalf("member %d: %+v", i, v)
				}
				if v.Class != map[string]string{"a": "holds", "b": "holds", "allow": "allows"}[v.Name] {
					t.Fatalf("class %+v", v)
				}
				if v.Violated && !strings.Contains(v.Trace, "Counterexample for "+v.Name+" ") {
					t.Fatalf("group trace attributed to member: %s", v.Trace)
				}
			}
			calls := batchInvocations(t, source)
			want := 3
			if failed != "" {
				want = 5
			}
			if len(calls) != want {
				t.Fatalf("calls=%v, want%d", calls, want)
			}
			first := calls[0]
			groupAt := slices.Index(first, "--invariants")
			if len(first) < 2 || first[0] != "verify" || !strings.HasSuffix(first[1], "simulate.qnt") || groupAt < 0 || !slices.Equal(first[groupAt+1:], []string{"b", "a"}) {
				t.Fatalf("batch command %v", first)
			}
			for _, call := range calls {
				at := slices.Index(call, "--max-steps")
				if at < 0 || call[at+1] != "3" {
					t.Fatalf("cap lost: %v", call)
				}
			}
			for _, name := range names {
				if !strings.Contains(progress.String(), "END invariant "+name+" status=completed") {
					t.Fatal(progress.String())
				}
			}
			if strings.Contains(progress.String(), "checking all members individually") != (failed != "") || strings.Contains(progress.String(), "uses shared batch proof") != (failed == "") {
				t.Fatalf("progress confuses shared proof with fallback: %s", progress.String())
			}
		})
	}
}

func TestNativeBatchFailuresYieldNoMemberProof(t *testing.T) {
	for _, tc := range []struct {
		name, result, trace string
		exit                int
	}{
		{"unknown", `{"stage":"verifying","status":"failure","errors":[]}`, "", 1},
		{"malformed", "{", "", 0},
		{"contradictory", nativeProofJSON, "", 1},
		{"missing-counterexample", nativeCexJSON, "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := batchFakeSource(t, nativeFakeScript(tc.result, tc.trace, tc.exit, "batch published"), nil)
			file := batchBinding(t, source, "{holds: [a, b]}")
			var progress bytes.Buffer
			report, err := Simulate(t.Context(), file, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 2, Progress: &progress})
			if err == nil || report != nil || len(batchInvocations(t, source)) != 1 || strings.Contains(progress.String(), "END invariant a status=completed") {
				t.Fatalf("report=%+v err=%v progress=%s", report, err, progress.String())
			}
		})
	}
}

type batchCancelProgress struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *batchCancelProgress) Write(b []byte) (int, error) {
	n, err := w.Buffer.Write(b)
	if strings.Contains(string(b), "batch published") {
		w.cancel()
	}
	return n, err
}
func TestNativeBatchCancellationDiscardsWrittenProof(t *testing.T) {
	source := batchFakeSource(t, nativeFakeScript(nativeProofJSON, "", 0, "batch published"), nil)
	file := batchBinding(t, source, "{holds: [a, b]}")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	progress := &batchCancelProgress{cancel: cancel}
	report, err := Simulate(ctx, file, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 2, Progress: progress})
	if !errors.Is(err, context.Canceled) || report != nil || len(batchInvocations(t, source)) != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestNativeBatchScopeAndSkip(t *testing.T) {
	for _, tc := range []struct {
		name, section string
		verify, calls int
	}{
		{"single-hold", "{holds: [a], allows: [allow]}", 2, 4},
		{"unclassified", "{}", 2, 4},
		{"sampling", "{holds: [a, b]}", 0, 4},
		{"verify-skip", "{holds: [a, b], verifySkip: 'not admitted'}", 2, 0},
		{"binding-skip", "{holds: [a, b], skip: 'not admitted'}", 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scalar := map[string]bool{"allow": tc.verify > 0}
			source := batchFakeSource(t, "echo unexpected-batch >&2\nexit 2\n", scalar)
			file := batchBinding(t, source, tc.section)
			report, err := Simulate(t.Context(), file, SimulateOptions{Quint: source.checker.Options.Quint, Verify: tc.verify})
			if err != nil || report == nil {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			if calls := batchInvocations(t, source); len(calls) != tc.calls {
				t.Fatalf("calls=%v want%d", calls, tc.calls)
			}
			if tc.verify == 0 {
				for _, v := range report.Invariants {
					if v.Bound.Proves() {
						t.Fatalf("sample became proof: %+v", v)
					}
				}
			}
		})
	}
}

func TestNativeBatchGroupNamesMustBeDistinct(t *testing.T) {
	for _, names := range [][]string{nil, {"a"}, {"a", "a"}, {"a", ""}} {
		if _, err := verifyNativeGroup(t.Context(), "unused", t.TempDir(), "unused.qnt", names, 2); err == nil {
			t.Fatalf("accepted %v", names)
		}
	}
}

func TestNativeBatchUsesSelectedInvariantsOnly(t *testing.T) {
	for _, names := range [][]string{{"a"}, {"b", "a"}, {"unclassified", "a"}} {
		t.Run(strings.Join(names, "-"), func(t *testing.T) {
			source := batchFakeSource(t, nativeFakeScript(nativeProofJSON, "", 0, "batch published"), nil)
			file := batchBinding(t, source, "{holds: [a, b]}")
			report, err := Simulate(t.Context(), file, SimulateOptions{Quint: source.checker.Options.Quint, Verify: 2, Invariants: names})
			if err != nil || report == nil || len(report.Invariants) != len(names) {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			for i, v := range report.Invariants {
				if v.Name != names[i] {
					t.Fatalf("member selection/order changed: %+v", report.Invariants)
				}
			}
			calls := batchInvocations(t, source)
			want := len(names)
			if slices.Equal(names, []string{"b", "a"}) {
				want = 1
			}
			if len(calls) != want {
				t.Fatalf("calls=%v, want %d", calls, want)
			}
		})
	}
}

// Opt-in native qualification compares exactly the same defined model and bound.
// Recorded order/cache/load effects prevent this tiny trial from establishing a
// controlled speedup; its primary assertion is equal bounded proof outcomes.
func TestNativeBatchPinnedPositiveComparison(t *testing.T) {
	if os.Getenv("GOTOGETHER_NATIVE_BATCH") != "1" {
		t.Skip("set GOTOGETHER_NATIVE_BATCH=1 for sequential tiny native comparison")
	}
	base := os.Getenv("GOTOGETHER_NATIVE_BATCH_ARTIFACTS")
	dir := t.TempDir()
	if base != "" {
		if err := os.MkdirAll(base, 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		dir, err = os.MkdirTemp(base, "positive-")
		if err != nil {
			t.Fatal(err)
		}
	}
	source := `module simulate {
 var x: int
 action init = x' = 0
 action step = x' = x + 1
 val nonnegative = x >= 0
 val bounded = x <= 2
}`
	file := filepath.Join(dir, "tiny.qnt")
	if err := os.WriteFile(file, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	wrapper := batchNativeRecorder(t, dir)
	for _, name := range []string{"nonnegative", "bounded"} {
		got, err := verifyNative(t.Context(), wrapper, dir, file, name, 2)
		if err != nil || got.violated {
			t.Fatalf("scalar %s: %+v %v", name, got, err)
		}
	}
	got, err := verifyNativeGroup(t.Context(), wrapper, dir, file, []string{"nonnegative", "bounded"}, 2)
	if err != nil || got.violated {
		t.Fatalf("batch: %+v %v", got, err)
	}
	calls, err := os.ReadFile(filepath.Join(dir, "commands.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	verified := 0
	for _, line := range bytes.Split(bytes.TrimSpace(calls), []byte{'\n'}) {
		var call struct {
			Argv []string `json:"argv"`
			Exit int      `json:"exit"`
		}
		if err := json.Unmarshal(line, &call); err != nil {
			t.Fatal(err)
		}
		if len(call.Argv) > 1 && call.Argv[1] == "verify" {
			verified++
			if call.Exit != 0 {
				t.Fatalf("native call failed: %s", line)
			}
		}
	}
	if verified != 3 {
		t.Fatalf("unexpected native verification count%d: %s", verified, calls)
	}
	receipt := map[string]any{"source_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(source))), "bound": 2, "invariants": []string{"nonnegative", "bounded"}, "verify_calls": verified, "order": "scalar nonnegative, scalar bounded, group conjunction", "qualification": "same-source same-bound native proof confirmation, not a controlled speedup; reaped child CPU is not guaranteed total backend CPU"}
	b, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "receipt.json"), append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("same model, bound2, two scalar proofs and one group proof; commands/artifacts in %s; no controlled speedup claim", dir)
}

func batchNativeRecorder(t *testing.T, dir string) string {
	t.Helper()
	actual, err := exec.LookPath("quint")
	if err != nil {
		t.Fatal(err)
	}
	actual, err = filepath.Abs(actual)
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(dir, "quint-recorder")
	script := `#!/usr/bin/env python3
import hashlib,json,os,resource,shutil,subprocess,sys,time
from pathlib import Path
base=Path(__file__).resolve().parent
actual=` + strconv.Quote(actual) + `
args=sys.argv[1:]
log=base/'commands.jsonl'
index=sum(1 for _ in log.open()) if log.exists() else 0
before=resource.getrusage(resource.RUSAGE_CHILDREN)
started=time.monotonic()
result=subprocess.run([actual,*args])
ended=time.monotonic()
after=resource.getrusage(resource.RUSAGE_CHILDREN)
artifacts={}
for flag in ('--out','--out-itf'):
 if flag in args:
  path=Path(args[args.index(flag)+1])
  if path.exists():
   kept=base/(str(index)+flag+'.json')
   shutil.copyfile(path,kept)
   artifacts[flag]={'path':str(kept),'sha256':hashlib.sha256(kept.read_bytes()).hexdigest()}
with log.open('a') as f:
 f.write(json.dumps({'argv':[actual,*args],'actual_resolved':str(Path(actual).resolve()),'actual_sha256':hashlib.sha256(Path(actual).read_bytes()).hexdigest(),'exit':result.returncode,'wall_seconds':ended-started,'reaped_child_cpu_seconds':(after.ru_utime+after.ru_stime)-(before.ru_utime+before.ru_stime),'artifacts':artifacts})+'\n')
 f.flush();os.fsync(f.fileno())
sys.exit(result.returncode)
`
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return wrapper
}

// This negative native control is separate from the positive comparison: a
// conjunction counterexample must cause every member to be checked and reported.
func TestNativeBatchPinnedCounterexampleFallback(t *testing.T) {
	if os.Getenv("GOTOGETHER_NATIVE_BATCH") != "1" {
		t.Skip("set GOTOGETHER_NATIVE_BATCH=1 for tiny native fallback confirmation")
	}
	dir := t.TempDir()
	if base := os.Getenv("GOTOGETHER_NATIVE_BATCH_ARTIFACTS"); base != "" {
		if err := os.MkdirAll(base, 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		dir, err = os.MkdirTemp(base, "negative-")
		if err != nil {
			t.Fatal(err)
		}
	}
	source := `module source {
 var x: int
 action init = x' = 0
 action step = x' = x + 1
 val a = x >= 0
 val b = x == 0
}`
	file := filepath.Join(dir, "tiny.qnt")
	if err := os.WriteFile(file, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	bindingFile := filepath.Join(dir, "tiny.yaml")
	binding := []byte("spec: tiny.qnt\nmodule: source\ninvariants: [a, b]\nsimulate: {holds: [a, b], verifySteps: 2}\n")
	if err := os.WriteFile(bindingFile, binding, 0600); err != nil {
		t.Fatal(err)
	}
	wrapper := batchNativeRecorder(t, dir)
	var progress bytes.Buffer
	report, err := Simulate(t.Context(), bindingFile, SimulateOptions{Quint: wrapper, Verify: 2, Progress: &progress})
	if err != nil || report == nil || report.Code != 1 || len(report.Invariants) != 2 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	a, b := report.Invariants[0], report.Invariants[1]
	if a.Name != "a" || !a.Held || a.Violated || a.Trace != "" || b.Name != "b" || b.Held || !b.Violated || !strings.Contains(b.Trace, "Counterexample for b ") {
		t.Fatalf("wrong individual attribution: %+v", report.Invariants)
	}
	for _, v := range report.Invariants {
		if v.Class != "holds" || v.Bound.Kind != "verified" || v.Bound.Steps != 2 {
			t.Fatalf("wrong member scope %+v", v)
		}
	}
	if !strings.Contains(progress.String(), "checking all members individually") || strings.Contains(progress.String(), "uses shared batch proof") {
		t.Fatalf("fallback progress: %s", progress.String())
	}
	calls, err := os.ReadFile(filepath.Join(dir, "commands.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	type call struct {
		Argv      []string                   `json:"argv"`
		Exit      int                        `json:"exit"`
		Artifacts map[string]json.RawMessage `json:"artifacts"`
	}
	var verification []call
	for _, line := range bytes.Split(bytes.TrimSpace(calls), []byte{'\n'}) {
		var c call
		if err := json.Unmarshal(line, &c); err != nil {
			t.Fatal(err)
		}
		if len(c.Argv) > 1 && c.Argv[1] == "verify" {
			verification = append(verification, c)
		}
	}
	if len(verification) != 3 {
		t.Fatalf("not every member rechecked: %s", calls)
	}
	first := verification[0]
	groupAt := slices.Index(first.Argv, "--invariants")
	if groupAt < 0 || !slices.Equal(first.Argv[groupAt+1:], []string{"a", "b"}) || first.Exit != 1 || first.Artifacts["--out-itf"] == nil {
		t.Fatalf("missing conjunction counterexample: %+v", first)
	}
	for i, name := range []string{"a", "b"} {
		c := verification[i+1]
		invAt := slices.Index(c.Argv, "--invariant")
		wantExit := i
		if invAt < 0 || c.Argv[invAt+1] != name || c.Exit != wantExit {
			t.Fatalf("wrong individual fallback: %+v", c)
		}
	}
	if verification[1].Artifacts["--out-itf"] != nil || verification[2].Artifacts["--out-itf"] == nil {
		t.Fatal("counterexample artifact attributed to wrong member")
	}
	saved, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), append(saved, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "progress.log"), progress.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	receipt := map[string]any{"source_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(source))), "binding_sha256": fmt.Sprintf("%x", sha256.Sum256(binding)), "bound": 2, "verify_calls": 3, "outcomes": map[string]string{"conjunction": "counterexample", "a": "holds", "b": "violated"}, "qualification": "tiny defined-source fallback confirmation only; no timing claim"}
	saved, err = json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "receipt.json"), append(saved, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("bound2 conjunction counterexample then every member checked; a holds, b violated; commands/ITF/report in %s", dir)
}
