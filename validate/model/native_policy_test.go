package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
)

// A fake producer inspects the actual owned config while the command is alive.
// Proof/error/cancellation classification stays with the existing strict tests.
func TestNativeOwnedAfterPolicy(t *testing.T) {
	for _, group := range []bool{false, true} {
		name := "scalar"
		if group {
			name = "group"
		}
		t.Run(name, func(t *testing.T) {
			metadata := filepath.Join(t.TempDir(), "config-evidence.json")
			script := `python3 - "$@" <<'PY'
import json,os,stat,sys
from pathlib import Path
args=sys.argv[1:]
p=Path(args[args.index('--apalache-config')+1])
assert args.count('--apalache-config')==1
if '--invariants' in args: assert args.index('--apalache-config') < args.index('--invariants')
assert json.loads(p.read_text())=={'checker':{'tuning':{'search.invariant.mode':'after'}}}
assert stat.S_IMODE(p.stat().st_mode)==0o600
assert p.parent.name.startswith('native-verify-')
Path(` + "'" + metadata + "'" + `).write_text(json.dumps({'path':str(p),'argv':args}))
PY
` + nativeFakeScript(nativeProofJSON, "", 0, "policy inspected")
			source := comparisonFakeSource(t, script)
			dir := t.TempDir()
			var err error
			if group {
				_, err = verifyNativeGroup(t.Context(), source.checker.Options.Quint, dir, "unused.qnt", []string{"a", "b"}, 2)
			} else {
				_, err = verifyNative(t.Context(), source.checker.Options.Quint, dir, "unused.qnt", "a", 2)
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(metadata)
			if err != nil {
				t.Fatal(err)
			}
			var evidence struct {
				Path string
				Argv []string
			}
			if err := json.Unmarshal(raw, &evidence); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(evidence.Path); !os.IsNotExist(err) {
				t.Fatalf("owned config survived command: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("owned workdir leaked: %v %v", entries, err)
			}
			if group {
				at := slices.Index(evidence.Argv, "--invariants")
				if at < 0 || strings.Join(evidence.Argv[at+1:], ",") != "a,b" {
					t.Fatalf("variadic arguments changed: %v", evidence.Argv)
				}
			}
		})
	}
}

// Opt-in complements the scalar producer controls with the same actual pinned
// backend, without a config-injecting wrapper or parallel SMT invocations.
func TestNativeAfterGroupedAndPossiblePinnedProducer(t *testing.T) {
	if os.Getenv("GOTOGETHER_NATIVE_VERIFY") != "1" {
		t.Skip("set GOTOGETHER_NATIVE_VERIFY=1 for tiny pinned native confirmations")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "tiny.qnt")
	source := `module simulate {
 var x: int
 action init = x' = 0
 action step = x' = x + 1
 val nonnegative = x >= 0
 val bounded = x <= 2
 val zero = x == 0
 val not_reachable = not(x >= 1)
 }`
	if err := os.WriteFile(file, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := verifyNativeGroup(t.Context(), "quint", dir, file, []string{"nonnegative", "bounded"}, 2)
	if err != nil || result.violated {
		t.Fatalf("group proof: %+v %v", result, err)
	}
	// A conjunction counterexample proves no particular member false. The
	// existing production fallback therefore leaves both scalar checks required.
	proved, err := proveNativeHolds(t.Context(), "quint", dir, file, []string{"nonnegative", "zero"}, map[string]bool{"nonnegative": true, "zero": true}, 2)
	if err != nil || len(proved) != 0 {
		t.Fatalf("group counterexample misattributed: %v %v", proved, err)
	}
	for _, name := range []string{"nonnegative", "zero"} {
		result, err := verifyNative(t.Context(), "quint", dir, file, name, 2)
		if err != nil || result.violated != (name == "zero") {
			t.Fatalf("fallback %s: %+v %v", name, result, err)
		}
	}
	possible := &possibleCheck{p: binding.Possible{Name: "reachable"}, when: "x >= 1", not: "not_reachable"}
	if err := quintPossibles(t.Context(), "quint", dir, file, []*possibleCheck{possible}, 2, 1, "1", 2, nil); err != nil || !possible.quintFound {
		t.Fatalf("predicate inversion: found=%v err=%v", possible.quintFound, err)
	}
}
