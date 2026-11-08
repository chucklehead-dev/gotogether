package grist_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
)

// Finite differential/oracle controls, not a proof for all finite histories.
// The oracle scans suffixes in Go, without either Quint function or matches.
func TestConditionHeadControls(t *testing.T) {
	requireQuint(t)
	type read struct {
		typ  string
		tags map[string]string
	}
	events := []read{{"a", nil}, {"a", map[string]string{"x": "a"}}, {"a", map[string]string{"x": "b"}}, {"a", map[string]string{"x": "a", "y": "b"}}, {"b", map[string]string{"x": "a"}}}
	logs := [][]read{nil}
	for _, e := range events {
		logs = append(logs, []read{e})
	}
	logs = append(logs, []read{events[1], events[2]}, []read{events[2], events[1]}, []read{events[4], events[3]}, []read{events[3], events[4]})
	reads := [][]read{nil, {{"a", nil}}, {{"a", map[string]string{"x": "a"}}}, {{"a", map[string]string{"x": "b"}}}, {{"a", map[string]string{"x": "a", "y": "b"}}}, {{"b", nil}}, {{"a", map[string]string{"": "a"}}}, {{"a", map[string]string{"x": "a"}}, {"b", nil}}}
	tags := func(ts map[string]string) string {
		var keys, parts []string
		for k := range ts {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("(%q,%q)", k, ts[k]))
		}
		return "Set(" + strings.Join(parts, ",") + ")"
	}
	oracle := func(log []read, pos int, rs []read) bool {
		if pos < 0 || pos > len(log) {
			return false
		}
		for _, e := range log[pos:] {
			for _, r := range rs {
				if e.typ != r.typ {
					continue
				}
				match := true
				for k, v := range r.tags {
					if got, ok := e.tags[k]; !ok || got != v {
						match = false
						break
					}
				}
				if match {
					return false
				}
			}
		}
		return true
	}
	dir := t.TempDir()
	lib, err := os.ReadFile("model/grist.qnt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "grist.qnt"), lib, 0600); err != nil {
		t.Fatal(err)
	}
	source := "module headControl {\n  import grist.* from \"./grist\"\n" + `
  pure def baseline(log: List[Event], readPos: int, reads: Set[Read]): bool = and {
    0 <= readPos,
    readPos <= log.length(),
    log.indices().forall(i => i < readPos or reads.forall(r => not(matches(log[i], r)))),
  }
  pure def optimized(log: List[Event], readPos: int, reads: Set[Read]): bool = conditionHolds(log, readPos, reads)
  var marker: int
  action init = marker' = 0
  action step = marker' = marker
`
	var logLiterals, readLiterals, expectedLogs []string
	for _, rs := range reads {
		var rr []string
		for _, r := range rs {
			rr = append(rr, fmt.Sprintf("{ typ: %q, tags: %s }", r.typ, tags(r.tags)))
		}
		readLiterals = append(readLiterals, "Set("+strings.Join(rr, ",")+")")
	}
	count := 0
	for _, log := range logs {
		var es, expectedReads []string
		for _, e := range log {
			es = append(es, fmt.Sprintf("{ typ: %q, strs: [], ints: [], tags: %s, cause: (\"\",\"\") }", e.typ, tags(e.tags)))
		}
		logLiterals = append(logLiterals, "["+strings.Join(es, ",")+"]")
		for _, rs := range reads {
			var positions []string
			for pos := -1; pos <= len(log)+1; pos++ {
				positions = append(positions, fmt.Sprintf("%t", oracle(log, pos, rs)))
				count++
			}
			expectedReads = append(expectedReads, "["+strings.Join(positions, ",")+"]")
		}
		expectedLogs = append(expectedLogs, "["+strings.Join(expectedReads, ",")+"]")
	}
	source += "  pure val logs: List[List[Event]] = [" + strings.Join(logLiterals, ",") + "]\n"
	source += "  pure val readSets: List[Set[Read]] = [" + strings.Join(readLiterals, ",") + "]\n"
	source += "  pure val expected: List[List[List[bool]]] = [" + strings.Join(expectedLogs, ",") + "]\n"
	source += "  run finiteOracleTest = assert(logs.indices().forall(li => readSets.indices().forall(ri => (-1).to(logs[li].length()+1).forall(pos => optimized(logs[li],pos,readSets[ri]) == expected[li][ri][pos+1] and baseline(logs[li],pos,readSets[ri]) == expected[li][ri][pos+1]))))\n"
	partial := []string{`([[]][1],0,Set())`, `([],[0][1],Set())`, `([],0,[Set({ typ: "a", tags: Set() })][1])`}
	for i, args := range partial {
		source += fmt.Sprintf("  pure val candidateError%d = conditionHolds%s\n  pure val originalError%d = baseline%s\n  run candidateError%dTest = assert(candidateError%d)\n  run originalError%dTest = assert(originalError%d)\n", i, args, i, args, i, i, i, i)
	}
	source += "}\n"
	file := filepath.Join(dir, "headControl.qnt")
	if err := os.WriteFile(file, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	ir := filepath.Join(dir, "ir.json")
	f, err := os.Create(ir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := quintcmd.Command(t.Context(), "quint", dir, "compile", "--target", "json", "--main", "headControl", "--init", "init", "--step", "step", file)
	cmd.Stdout = f
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err = cmd.Run()
	f.Close()
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, stderr.String())
	}
	b, err := os.ReadFile(ir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := qeval.Load(b)
	if err != nil {
		t.Fatal(err)
	}
	logValues, err := m.Eval(nil, nil, "logs")
	if err != nil {
		t.Fatal(err)
	}
	readValues, err := m.Eval(nil, nil, "readSets")
	if err != nil {
		t.Fatal(err)
	}
	expectedValues, err := m.Eval(nil, nil, "expected")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for li, log := range logValues.(qeval.List) {
		for ri, rs := range readValues.(qeval.List) {
			for pi, want := range expectedValues.(qeval.List)[li].(qeval.List)[ri].(qeval.List) {
				args := []qeval.Value{log, qeval.Int(pi - 1), rs}
				a, ae := m.Eval(nil, nil, "optimized", args...)
				b, be := m.Eval(nil, nil, "baseline", args...)
				if ae != nil || be != nil || qeval.Compare(a, want) != 0 || qeval.Compare(b, want) != 0 {
					t.Fatalf("case %d/%d/%d mismatch: %v/%v expected %v errors %v/%v", li, ri, pi, a, b, want, ae, be)
				}
				checked++
			}
		}
	}
	if checked != count {
		t.Fatalf("got%d cases, want%d", checked, count)
	}
	for i := range partial {
		_, a := m.Eval(nil, nil, fmt.Sprintf("candidateError%d", i))
		_, b := m.Eval(nil, nil, fmt.Sprintf("originalError%d", i))
		if a == nil || reflect.TypeOf(a) != reflect.TypeOf(b) || a.Error() != b.Error() {
			t.Fatalf("partial argument%d changed: %v / %v", i, a, b)
		}
	}
	// Explicit TypeScript backend: no Rust simulation, SMT checker or server.
	cmd = quintcmd.Command(t.Context(), "quint", dir, "test", file, "--main", "headControl", "--backend", "typescript", "--match", "(finiteOracleTest|Error[0-2]Test)$", "--max-samples", "1", "--seed", "1")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "ok finiteOracleTest passed 1 test(s)") || strings.Count(string(out), "Error [QNT510]: Out of bounds, nth(1)") != 6 {
		t.Fatalf("TypeScript partial argument controls: err=%v\n%s", err, out)
	}
	for i := range partial {
		for _, prefix := range []string{"candidate", "original"} {
			if !strings.Contains(string(out), fmt.Sprintf("%sError%dTest failed", prefix, i)) {
				t.Fatalf("missing TypeScript partial argument control: %s%d\n%s", prefix, i, out)
			}
		}
	}
	t.Logf("%d finite value cases, three eager-argument error pairs, Go evaluator and TypeScript oracle; not universal proof", count)
}
