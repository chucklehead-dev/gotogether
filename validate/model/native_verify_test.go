package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/itf"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const nativeProofJSON = `{"stage":"verifying","status":"ok","errors":[]}`
const nativeCexJSON = `{"stage":"verifying","status":"violation","errors":[],"trace":[{"id":0,"kind":"app","opcode":"Rec","args":[{"id":0,"kind":"str","value":"x"},{"id":0,"kind":"int","value":0}]}]}`
const nativeCexITF = `{"vars":["x"],"states":[{"x":{"#bigint":"0"}}]}`

func nativeFakeScript(result, trace string, exit int, console string) string {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
	// Publish artifacts before diagnostics, so cancellation triggered by the
	// progress sink really races an already written valid result.
	return "native_args=\"$*\"\nout=''\nitf=''\nwhile [ $# -gt 0 ]; do\n case \"$1\" in\n --out) out=$2; shift;;\n --out-itf) itf=$2; shift;;\n esac\n shift\ndone\n" +
		"if [ -n \"$out\" ]; then printf '%s' " + quote(result) + " > \"$out\"; fi\n" +
		"if [ -n \"$itf\" ] && [ -n " + quote(trace) + " ]; then printf '%s' " + quote(trace) + " > \"$itf\"; fi\n" +
		"echo " + quote(console) + "\necho \"native argv: $native_args\"\n" + fmt.Sprintf("exit %d\n", exit)
}

func TestNativeStructuredVerdicts(t *testing.T) {
	tests := []struct {
		name, result, trace string
		exit, steps         int
		violated, valid     bool
	}{
		{"proof", nativeProofJSON, "", 0, 2, false, true},
		{"counterexample", nativeCexJSON, nativeCexITF, 1, 2, true, true},
		{"initial-counterexample", nativeCexJSON, nativeCexITF, 1, 0, true, true},
		{"proof-wrong-exit", nativeProofJSON, "", 1, 2, false, false},
		{"cex-wrong-exit", nativeCexJSON, nativeCexITF, 0, 2, false, false},
		{"proof-errors", strings.Replace(nativeProofJSON, `"errors":[]`, `"errors":[{"message":"unknown"}]`, 1), "", 0, 2, false, false},
		{"cex-errors", strings.Replace(nativeCexJSON, `"errors":[]`, `"errors":[{"message":"undefined"}]`, 1), nativeCexITF, 1, 2, false, false},
		{"proof-trace", strings.Replace(nativeProofJSON, `"errors":[]`, `"errors":[],"trace":null`, 1), "", 0, 2, false, false},
		{"proof-itf", nativeProofJSON, nativeCexITF, 0, 2, false, false},
		{"empty-itf-file", nativeProofJSON, "", 0, 2, false, true},
		{"missing-itf", nativeCexJSON, "", 1, 2, false, false},
		{"malformed-itf", nativeCexJSON, `{`, 1, 2, false, false},
		{"mismatching-itf", nativeCexJSON, strings.Replace(nativeCexITF, `"0"`, `"1"`, 1), 1, 2, false, false},
		{"no-itf-vars", nativeCexJSON, `{"states":[{}]}`, 1, 2, false, false},
		{"undeclared-itf-var", nativeCexJSON, `{"vars":[],"states":[{"x":{"#bigint":"0"}}]}`, 1, 2, false, false},
		{"missing-var-value", nativeCexJSON, `{"vars":["x"],"states":[{}]}`, 1, 2, false, false},
		{"empty-trace", strings.Replace(nativeCexJSON, `[{"id":0,"kind":"app","opcode":"Rec","args":[{"id":0,"kind":"str","value":"x"},{"id":0,"kind":"int","value":0}]}]`, `[]`, 1), nativeCexITF, 1, 2, false, false},
		{"unknown-status", strings.Replace(nativeProofJSON, `"ok"`, `"unknown"`, 1), "", 0, 2, false, false},
		{"earlier-stage", strings.Replace(nativeProofJSON, `"verifying"`, `"parsing"`, 1), "", 0, 2, false, false},
		{"missing-errors", `{"stage":"verifying","status":"ok"}`, "", 0, 2, false, false},
		{"null-errors", strings.Replace(nativeProofJSON, `[]`, `null`, 1), "", 0, 2, false, false},
		{"string-errors", strings.Replace(nativeProofJSON, `[]`, `""`, 1), "", 0, 2, false, false},
		{"duplicate-key", strings.Replace(nativeProofJSON, `"status":"ok"`, `"status":"ok","status":"ok"`, 1), "", 0, 2, false, false},
		{"case-alias", strings.Replace(nativeProofJSON, `"status"`, `"Status"`, 1), "", 0, 2, false, false},
		{"case-alias-with-canonical", strings.Replace(nativeProofJSON, `"status":"ok"`, `"status":"ok","STATUS":"failure"`, 1), "", 0, 2, false, false},
		{"duplicate-ignored-data", strings.Replace(nativeProofJSON, `"errors":[]`, `"errors":[],"modules":{"x":0,"x":1}`, 1), "", 0, 2, false, false},
		{"torn-json", nativeProofJSON[:len(nativeProofJSON)-1], "", 0, 2, false, false},
		{"multiple-json", nativeProofJSON + nativeProofJSON, "", 0, 2, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var trace []byte
			if tt.trace != "" {
				trace = []byte(tt.trace)
			}
			got, err := classifyNative([]byte(tt.result), trace, tt.exit, tt.steps)
			if (err == nil) != tt.valid || err == nil && got.violated != tt.violated {
				t.Fatalf("result=%+v err=%v", got, err)
			}
		})
	}
	if _, err := classifyNative([]byte(nativeProofJSON), []byte{}, 0, 2); err == nil {
		t.Fatal("empty preexisting ITF file accepted as proof")
	}
}

func TestNativeRequiredDiscriminators(t *testing.T) {
	for _, field := range []string{"stage", "status", "errors"} {
		for _, null := range []bool{false, true} {
			var m map[string]any
			if err := json.Unmarshal([]byte(nativeProofJSON), &m); err != nil {
				t.Fatal(err)
			}
			if null {
				m[field] = nil
			} else {
				delete(m, field)
			}
			b, _ := json.Marshal(m)
			if _, err := classifyNative(b, nil, 0, 2); err == nil {
				t.Fatalf("accepted absent/null %s", field)
			}
		}
	}
	invalid := append([]byte(`{"stage":"verifying","status":"ok","errors":[],"unused":"`), byte(0xff))
	invalid = append(invalid, []byte(`"}`)...)
	if _, err := classifyNative(invalid, nil, 0, 2); err == nil {
		t.Fatal("invalid UTF-8 became proof")
	}
}

func TestNativeTracePreservesCaseSensitiveFieldsAndBound(t *testing.T) {
	var cex map[string]any
	if err := json.Unmarshal([]byte(nativeCexJSON), &cex); err != nil {
		t.Fatal(err)
	}
	state := cex["trace"].([]any)[0].(map[string]any)
	args := state["args"].([]any)
	state["args"] = append(args, map[string]any{"id": 0, "kind": "str", "value": "X"}, map[string]any{"id": 0, "kind": "int", "value": 1})
	b, _ := json.Marshal(cex)
	trace := `{"vars":["X","x"],"states":[{"X":{"#bigint":"1"},"x":{"#bigint":"0"}}]}`
	if _, err := classifyNative(b, []byte(trace), 1, 2); err != nil {
		t.Fatal(err)
	}
	cex["trace"] = []any{state, state}
	b, _ = json.Marshal(cex)
	trace = `{"vars":["X","x"],"states":[{"X":{"#bigint":"1"},"x":{"#bigint":"0"}},{"X":{"#bigint":"1"},"x":{"#bigint":"0"}}]}`
	if _, err := classifyNative(b, []byte(trace), 1, 0); err == nil {
		t.Fatal("over-bound counterexample accepted")
	}
}

func TestNativeProcessEvidenceAndFreshArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name, result, trace, console string
		exit                         int
		valid, violated              bool
	}{
		{"proof", nativeProofJSON, "", "[violation] misleading text", 0, true, false},
		{"cex", nativeCexJSON, nativeCexITF, "[ok] misleading text", 1, true, true},
		{"no-result", "", "", "[ok]", 0, false, false},
		{"forged-marker", `{"stage":"verifying","status":"failure","errors":[]}`, "", "[violation]", 1, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := comparisonFakeSource(t, nativeFakeScript(tc.result, tc.trace, tc.exit, tc.console))
			dir := t.TempDir()
			// An earlier adjacent artifact must not be picked up by a new invocation.
			if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte(nativeProofJSON), 0600); err != nil {
				t.Fatal(err)
			}
			var progress bytes.Buffer
			ctx := context.WithValue(t.Context(), simulationProgressKey{}, &simulationProgress{w: &progress})
			got, err := verifyNative(ctx, source.checker.Options.Quint, dir, "unused.qnt", "safe", 2)
			if (err == nil) != tc.valid || err == nil && got.violated != tc.violated {
				t.Fatalf("result=%+v err=%v", got, err)
			}
			if tc.violated && !strings.Contains(got.diagnostic, `"#bigint": "0"`) {
				t.Fatalf("counterexample content lost: %s", got.diagnostic)
			}
			if !strings.Contains(progress.String(), tc.console) {
				t.Fatal("emitted native diagnostics not streamed")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary invocation artifacts leaked: %v %v", entries, err)
			}
		})
	}
}

func TestNativeTraceAllLiteralKinds(t *testing.T) {
	n := itf.Value{Kind: itf.Int, I: new(big.Int).Exp(big.NewInt(2), big.NewInt(100), nil)}
	text := itf.Value{Kind: itf.Str, S: "case"}
	payload := itf.Value{Kind: itf.Bool, B: true}
	value := itf.Value{Kind: itf.Record, Fields: map[string]itf.Value{
		"large": n, "text": text, "flag": payload,
		"list":    {Kind: itf.List, Elems: []itf.Value{n, text}},
		"set":     {Kind: itf.Set, Elems: []itf.Value{text}},
		"tuple":   {Kind: itf.Tuple, Elems: []itf.Value{payload, n}},
		"map":     {Kind: itf.Map, Pairs: [][2]itf.Value{{text, n}}},
		"variant": {Kind: itf.Variant, Tag: "Some", Inner: &payload},
	}}
	var expr func(itf.Value) any
	expr = func(v itf.Value) any {
		m := map[string]any{"id": 0}
		switch v.Kind {
		case itf.Int:
			m["kind"] = "int"
			m["value"] = json.Number(v.I.String())
		case itf.Str:
			m["kind"] = "str"
			m["value"] = v.S
		case itf.Bool:
			m["kind"] = "bool"
			m["value"] = v.B
		default:
			m["kind"] = "app"
			args := []any{}
			switch v.Kind {
			case itf.List, itf.Set, itf.Tuple:
				m["opcode"] = map[itf.Kind]string{itf.List: "List", itf.Set: "Set", itf.Tuple: "Tup"}[v.Kind]
				for _, e := range v.Elems {
					args = append(args, expr(e))
				}
			case itf.Record:
				m["opcode"] = "Rec"
				for name, e := range v.Fields {
					args = append(args, expr(itf.Value{Kind: itf.Str, S: name}), expr(e))
				}
			case itf.Map:
				m["opcode"] = "Map"
				for _, p := range v.Pairs {
					args = append(args, expr(itf.Value{Kind: itf.Tuple, Elems: []itf.Value{p[0], p[1]}}))
				}
			case itf.Variant:
				m["opcode"] = "variant"
				args = []any{expr(itf.Value{Kind: itf.Str, S: v.Tag}), expr(*v.Inner)}
			}
			m["args"] = args
		}
		return m
	}
	state := itf.Value{Kind: itf.Record, Fields: map[string]itf.Value{"x": value}}
	result, _ := json.Marshal(map[string]any{"stage": "verifying", "status": "violation", "errors": []any{}, "trace": []any{expr(state)}})
	trace, _ := json.Marshal(map[string]any{"vars": []string{"x"}, "states": []any{state}})
	if _, err := classifyNative(result, trace, 1, 2); err != nil {
		t.Fatal(err)
	}
	value.Fields["flag"] = itf.Value{Kind: itf.Bool, B: false}
	trace, _ = json.Marshal(map[string]any{"vars": []string{"x"}, "states": []any{state}})
	if _, err := classifyNative(result, trace, 1, 2); err == nil {
		t.Fatal("changed literal value accepted")
	}
}

// Explicit opt-in keeps the ordinary suite independent of Java/SMT installation.
// The pinned producer is confirmed separately with a tiny fixed bound. Valid
// examples use defined expressions: Quint explicitly leaves out-of-range nth
// undefined, so successful native verification cannot diagnose that source error.
func TestNativeVerificationPinnedProducer(t *testing.T) {
	if os.Getenv("GOTOGETHER_NATIVE_VERIFY") != "1" {
		t.Skip("set GOTOGETHER_NATIVE_VERIFY=1 for tiny pinned native confirmations")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "tiny.qnt")
	source := `module simulate {
 var x: int
 action init = x' = 0
 action step = x' = x + 1
 val safe = x <= 2
 val violated = x == 0
 val initial = x < 0
 }`
	if err := os.WriteFile(file, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name            string
		violated, valid bool
	}{
		{"safe", false, true}, {"violated", true, true}, {"initial", true, true}, {"unknownInvariant", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := verifyNative(t.Context(), "quint", dir, file, tc.name, 2)
			if (err == nil) != tc.valid || err == nil && got.violated != tc.violated {
				t.Fatalf("result=%+v err=%v", got, err)
			}
			if tc.violated && !strings.Contains(got.diagnostic, "Counterexample for "+tc.name) {
				t.Fatalf("missing human counterexample: %s", got.diagnostic)
			}
		})
	}
}

func TestNativeVerifiedPossibleInversion(t *testing.T) {
	for _, tc := range []struct {
		name, result, trace string
		exit                int
		found, valid        bool
	}{
		{"bounded-impossible", nativeProofJSON, "", 0, false, true},
		{"witness", nativeCexJSON, nativeCexITF, 1, true, true},
		{"backend-failure", `{"stage":"verifying","status":"failure","errors":[]}`, "", 1, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := comparisonFakeSource(t, nativeFakeScript(tc.result, tc.trace, tc.exit, "[violation] irrelevant console"))
			c := &possibleCheck{p: binding.Possible{Name: "reachable"}, when: "true", not: "not_reachable"}
			err := quintPossibles(t.Context(), source.checker.Options.Quint, t.TempDir(), "unused.qnt", []*possibleCheck{c}, 1, 1, "1", 2, nil)
			if (err == nil) != tc.valid || c.quintFound != tc.found {
				t.Fatalf("found=%v err=%v", c.quintFound, err)
			}
		})
	}
}

type nativeCancelSink struct{ cancel context.CancelFunc }

func (w nativeCancelSink) Write(b []byte) (int, error) { w.cancel(); return len(b), nil }
func TestNativeCancellationWinsValidResult(t *testing.T) {
	source := comparisonFakeSource(t, nativeFakeScript(nativeProofJSON, "", 0, "completed output"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx = context.WithValue(ctx, simulationProgressKey{}, &simulationProgress{w: nativeCancelSink{cancel}})
	result, err := verifyNative(ctx, source.checker.Options.Quint, t.TempDir(), "unused.qnt", "safe", 2)
	if !errors.Is(err, context.Canceled) || result.violated {
		t.Fatalf("cancellation became result: %+v %v", result, err)
	}
}
