package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/chucklehead-dev/gotogether/itf"
)

type nativeVerification struct {
	violated   bool
	diagnostic string
}

// verifyNative uses Quint's supported stage result, never console markers, as
// bounded safety evidence. Each invocation owns fresh result/trace paths.
// Quint suppresses its console checker progress with --out; our parent phases
// still report START/END, and any emitted diagnostics remain streamed.
func verifyNative(ctx context.Context, quint, dir, file, invariant string, steps int) (nativeVerification, error) {
	return verifyNativeInvariants(ctx, quint, dir, file, []string{invariant}, steps)
}

// verifyNativeGroup checks one conjunction under exactly one native invocation.
// A counterexample is evidence only against the conjunction, not any named member.
func verifyNativeGroup(ctx context.Context, quint, dir, file string, invariants []string, steps int) (nativeVerification, error) {
	if len(invariants) < 2 {
		return nativeVerification{}, fmt.Errorf("native invariant group requires at least two members")
	}
	return verifyNativeInvariants(ctx, quint, dir, file, invariants, steps)
}

func verifyNativeInvariants(ctx context.Context, quint, dir, file string, invariants []string, steps int) (nativeVerification, error) {
	if len(invariants) == 0 {
		return nativeVerification{}, fmt.Errorf("native verification requires an invariant")
	}
	invariant := strings.Join(invariants, ", ")
	seen := map[string]bool{}
	for _, name := range invariants {
		if strings.TrimSpace(name) == "" || seen[name] {
			return nativeVerification{}, fmt.Errorf("native invariants require distinct nonempty names")
		}
		seen[name] = true
	}
	work, err := os.MkdirTemp(dir, "native-verify-")
	if err != nil {
		return nativeVerification{}, err
	}
	defer os.RemoveAll(work)
	resultFile, traceFile := filepath.Join(work, "result.json"), filepath.Join(work, "trace.itf.json")
	argv := []string{"verify", file, "--main", "simulate", "--invariant", invariant, "--max-steps", fmt.Sprint(steps), "--out", resultFile, "--out-itf", traceFile}
	if len(invariants) > 1 {
		// --invariants is variadic: keep the input file and all other options
		// before its members, so none can be interpreted as another invariant.
		argv = []string{"verify", file, "--main", "simulate", "--max-steps", fmt.Sprint(steps), "--out", resultFile, "--out-itf", traceFile, "--invariants"}
		argv = append(argv, invariants...)
	}
	out, commandErr := runQuint(ctx, quint, dir, argv)
	if ctx.Err() != nil {
		return nativeVerification{}, ctx.Err()
	}
	if isAnalysisCancellation(commandErr) {
		return nativeVerification{}, commandErr
	}
	exit := 0
	if commandErr != nil {
		e, ok := errors.AsType[*exec.ExitError](commandErr)
		if !ok || e.ExitCode() != 1 {
			return nativeVerification{}, fmt.Errorf("quint verify %s: %w\n%s", invariant, commandErr, out)
		}
		exit = e.ExitCode()
	}
	data, err := os.ReadFile(resultFile)
	if err != nil {
		return nativeVerification{}, fmt.Errorf("quint verify %s: read structured result: %w\n%s", invariant, err, out)
	}
	trace, traceErr := os.ReadFile(traceFile)
	if traceErr != nil && !errors.Is(traceErr, os.ErrNotExist) {
		return nativeVerification{}, fmt.Errorf("quint verify %s: read trace: %w", invariant, traceErr)
	}
	result, err := classifyNative(data, trace, exit, steps)
	if ctx.Err() != nil {
		return nativeVerification{}, ctx.Err()
	}
	if err != nil {
		return nativeVerification{}, fmt.Errorf("quint verify %s: %w\n%s\nstructured result: %s", invariant, err, out, nativeDiagnostic(data))
	}
	result.diagnostic = out
	if result.violated {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, trace, "", "  "); err != nil {
			return nativeVerification{}, err
		}
		result.diagnostic += fmt.Sprintf("\nCounterexample for %s (quint verify, ITF):\n", invariant) + pretty.String()
	}
	return result, nil
}

func nativeDiagnostic(data []byte) string {
	// Keep structured backend errors available without dumping the compiled
	// modules/type tables that Quint includes in --out.
	v, err := uniqueJSON(data)
	if err != nil {
		return string(data)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return string(data)
	}
	b, _ := json.Marshal(map[string]any{"stage": m["stage"], "status": m["status"], "errors": m["errors"]})
	return string(b)
}

func classifyNative(data, trace []byte, exit, steps int) (nativeVerification, error) {
	v, err := uniqueJSON(data)
	if err != nil {
		return nativeVerification{}, fmt.Errorf("invalid structured result: %w", err)
	}
	m, err := nativeObject(v, "stage", "status", "errors", "trace")
	if err != nil {
		return nativeVerification{}, err
	}
	stage, ok := m["stage"].(string)
	if !ok || stage != "verifying" {
		return nativeVerification{}, fmt.Errorf("expected verifying stage, got %v", m["stage"])
	}
	status, ok := m["status"].(string)
	if !ok {
		return nativeVerification{}, fmt.Errorf("missing or invalid verification status")
	}
	es, ok := m["errors"].([]any)
	if !ok {
		return nativeVerification{}, fmt.Errorf("missing or invalid verification errors")
	}
	switch status {
	case "ok":
		_, hasTrace := m["trace"]
		if exit != 0 || len(es) != 0 || hasTrace || trace != nil {
			return nativeVerification{}, fmt.Errorf("contradictory verification proof/exit/errors/trace")
		}
		return nativeVerification{}, nil
	case "violation":
		if exit != 1 || len(es) != 0 {
			return nativeVerification{}, fmt.Errorf("counterexample requires actual exit 1 and no backend errors")
		}
		if err := validateNativeTrace(m["trace"], trace, steps); err != nil {
			return nativeVerification{}, err
		}
		return nativeVerification{violated: true}, nil
	default:
		return nativeVerification{}, fmt.Errorf("verification did not complete: status %q", status)
	}
}

// nativeObject only checks aliases of schema discriminators. Model record and
// variable names are case-sensitive data, and may legitimately include X and x.
func nativeObject(v any, reserved ...string) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected JSON object")
	}
	for key := range m {
		for _, name := range reserved {
			if key != name && strings.EqualFold(key, name) {
				return nil, fmt.Errorf("ambiguous field %q", key)
			}
		}
	}
	return m, nil
}

// uniqueJSON preserves case-sensitive data keys, but rejects duplicate keys at
// every depth and trailing JSON values, including in ignored stage payloads.
func uniqueJSON(b []byte) (any, error) {
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("invalid UTF-8 JSON")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var read func() (any, error)
	read = func() (any, error) {
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t {
		case json.Delim('{'):
			m := map[string]any{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return nil, err
				}
				key, ok := k.(string)
				if !ok {
					return nil, fmt.Errorf("invalid object key")
				}
				if _, ok := m[key]; ok {
					return nil, fmt.Errorf("duplicate field %q", key)
				}
				value, err := read()
				if err != nil {
					return nil, err
				}
				m[key] = value
			}
			_, err = d.Token()
			return m, err
		case json.Delim('['):
			a := []any{}
			for d.More() {
				v, err := read()
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			_, err = d.Token()
			return a, err
		default:
			return t, nil
		}
	}
	v, err := read()
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON data")
	}
	return v, nil
}

func validateNativeTrace(normalized any, data []byte, steps int) error {
	states, ok := normalized.([]any)
	if !ok || len(states) == 0 || len(states)-1 > steps {
		return fmt.Errorf("counterexample has no normalized trace")
	}
	raw, err := uniqueJSON(data)
	if err != nil {
		return fmt.Errorf("invalid counterexample ITF: %w", err)
	}
	m, err := nativeObject(raw, "vars", "states")
	if err != nil {
		return err
	}
	vars, ok := m["vars"].([]any)
	if !ok {
		return fmt.Errorf("counterexample ITF has no vars array")
	}
	names := map[string]bool{}
	for _, v := range vars {
		name, ok := v.(string)
		if !ok || names[name] {
			return fmt.Errorf("invalid counterexample ITF variables")
		}
		names[name] = true
	}
	rawStates, ok := m["states"].([]any)
	if !ok || len(rawStates) != len(states) {
		return fmt.Errorf("counterexample trace lengths disagree")
	}
	parsed, err := itf.Parse(data)
	if err != nil || len(parsed.States) != len(states) {
		return fmt.Errorf("invalid counterexample ITF values: %v", err)
	}
	for i, state := range states {
		value, err := nativeLiteral(state)
		if err != nil || value.Kind != itf.Record {
			return fmt.Errorf("invalid normalized state %d: %v", i, err)
		}
		rs, ok := rawStates[i].(map[string]any)
		if !ok {
			return fmt.Errorf("invalid ITF state %d", i)
		}
		for name := range names {
			if _, ok := rs[name]; !ok {
				return fmt.Errorf("ITF state %d missing variable %s", i, name)
			}
		}
		count := 0
		for name, v := range rs {
			if name == "#meta" || strings.HasPrefix(name, "__") {
				continue
			}
			if !names[name] {
				return fmt.Errorf("ITF state %d has undeclared variable %s", i, name)
			}
			count++
			// Decode under a neutral variable name so module-qualified source names
			// remain distinct rather than being stripped by the general ITF reader.
			b, _ := json.Marshal(map[string]any{"vars": []string{"v"}, "states": []any{map[string]any{"v": v}}})
			one, err := itf.Parse(b)
			if err != nil {
				return fmt.Errorf("invalid ITF state %d field %s: %w", i, name, err)
			}
			got, ok := value.Fields[name]
			if !ok || !got.Equal(one.States[0].Vars["v"]) {
				return fmt.Errorf("counterexample states disagree at %d field %s", i, name)
			}
		}
		if count != len(value.Fields) {
			return fmt.Errorf("counterexample state %d fields disagree", i)
		}
	}
	return nil
}

func nativeLiteral(v any) (itf.Value, error) {
	m, err := nativeObject(v, "id", "kind", "value", "opcode", "args")
	if err != nil {
		return itf.Value{}, err
	}
	bad := func() (itf.Value, error) { return itf.Value{}, fmt.Errorf("invalid normalized Quint literal") }
	id, ok := m["id"].(json.Number)
	if !ok {
		return bad()
	}
	if _, err := id.Int64(); err != nil {
		return bad()
	}
	switch m["kind"] {
	case "int":
		n, ok := m["value"].(json.Number)
		if !ok {
			return bad()
		}
		i, ok := new(big.Int).SetString(n.String(), 10)
		if !ok {
			return bad()
		}
		return itf.Value{Kind: itf.Int, I: i}, nil
	case "str":
		s, ok := m["value"].(string)
		if !ok {
			return bad()
		}
		return itf.Value{Kind: itf.Str, S: s}, nil
	case "bool":
		b, ok := m["value"].(bool)
		if !ok {
			return bad()
		}
		return itf.Value{Kind: itf.Bool, B: b}, nil
	case "app":
		args, ok := m["args"].([]any)
		if !ok {
			return bad()
		}
		vs := make([]itf.Value, len(args))
		for i, a := range args {
			vs[i], err = nativeLiteral(a)
			if err != nil {
				return bad()
			}
		}
		switch m["opcode"] {
		case "List":
			return itf.Value{Kind: itf.List, Elems: vs}, nil
		case "Set":
			return itf.Value{Kind: itf.Set, Elems: vs}, nil
		case "Tup":
			return itf.Value{Kind: itf.Tuple, Elems: vs}, nil
		case "Map":
			ps := make([][2]itf.Value, len(vs))
			for i, v := range vs {
				if v.Kind != itf.Tuple || len(v.Elems) != 2 {
					return bad()
				}
				ps[i] = [2]itf.Value{v.Elems[0], v.Elems[1]}
			}
			return itf.Value{Kind: itf.Map, Pairs: ps}, nil
		case "Rec":
			if len(vs)%2 != 0 {
				return bad()
			}
			fs := map[string]itf.Value{}
			for i := 0; i < len(vs); i += 2 {
				if vs[i].Kind != itf.Str {
					return bad()
				}
				if _, ok := fs[vs[i].S]; ok {
					return bad()
				}
				fs[vs[i].S] = vs[i+1]
			}
			return itf.Value{Kind: itf.Record, Fields: fs}, nil
		case "variant":
			if len(vs) != 2 || vs[0].Kind != itf.Str {
				return bad()
			}
			return itf.Value{Kind: itf.Variant, Tag: vs[0].S, Inner: &vs[1]}, nil
		}
	}
	return bad()
}
