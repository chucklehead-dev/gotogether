package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustInventory(t *testing.T, text string) []mutant {
	t.Helper()
	result, err := inventory(text)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAdmissionMapsLocalIDsAndAggregates(t *testing.T) {
	// Init order differs: choice.go starts at 1 in root and at 0 in child.
	// The unrelated file is absent from child, not evidence that it was killed.
	root := suite{pkg: "qenv", mutants: mustInventory(t, `qmut: mutant 0 qenv/root.go:3 root: drop call (f())
qmut: mutant 1 qenv/internal/choice/rates.go:10 checked.of: drop call (r.check())
`)}
	child := suite{pkg: "qenv/internal/choice", mutants: mustInventory(t, `qmut: mutant 0 qenv/internal/choice/rates.go:10 checked.of: drop call (r.check())
`)}
	baselines := 0
	candidates, err := admit([]suite{root, child, {pkg: "unlinked-test-suite"}}, func(s suite) error { baselines++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if baselines != 3 || len(candidates) != 2 {
		t.Fatalf("baselines %d candidates %v", baselines, candidates)
	}
	for _, c := range candidates {
		var calls []target
		passed, err := survives(c, func(t target) (bool, error) { calls = append(calls, t); return t.suite == 0, nil })
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(c.file, "choice") {
			if passed || len(calls) != 2 || calls[0] != (target{0, 1}) || calls[1] != (target{1, 0}) {
				t.Fatalf("wrong local activation/aggregation: %v, %v", passed, calls)
			}
		} else if !passed || len(calls) != 1 || calls[0] != (target{0, 0}) {
			t.Fatalf("absent binary treated as kill or scheduled: %v, %v", passed, calls)
		}
	}
}

func TestIdenticalDescriptorsRemainDistinct(t *testing.T) {
	mutants := mustInventory(t, `qmut: mutant 0 qenv/a.go:3 f: drop call (f())
qmut: mutant 1 qenv/a.go:3 f: drop call (f())
`)
	candidates, err := admit([]suite{{pkg: "a", mutants: mutants}, {pkg: "b", mutants: mutants}}, func(suite) error { return nil })
	if err != nil || len(candidates) != 2 {
		t.Fatalf("collapsed repeated source sites: %v, %v", candidates, err)
	}
	for _, c := range candidates {
		if len(c.targets) != 2 {
			t.Fatalf("missing target: %v", c)
		}
	}
}

func TestAdmissionFailsClosed(t *testing.T) {
	a := mustInventory(t, "qmut: mutant 0 qenv/a.go:1 f: drop call (a())\n")
	b := mustInventory(t, "qmut: mutant 0 qenv/a.go:2 f: drop call (b())\n")
	longer := mustInventory(t, "qmut: mutant 0 qenv/a.go:1 f: drop call (a())\nqmut: mutant 1 qenv/a.go:2 f: drop call (b())\n")
	for _, tc := range []struct {
		name     string
		suites   []suite
		baseline func(suite) error
	}{
		{"baseline", []suite{{pkg: "bad", mutants: a}}, func(suite) error { return errors.New("failed") }},
		{"descriptor", []suite{{mutants: a}, {mutants: b}}, func(suite) error { return nil }},
		{"file count", []suite{{mutants: a}, {mutants: longer}}, func(suite) error { return nil }},
		{"empty inventory", []suite{{pkg: "empty"}}, func(suite) error { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := admit(tc.suites, tc.baseline); err == nil {
				t.Fatal("admitted invalid campaign")
			}
		})
	}
}

func TestInventoryFailsClosed(t *testing.T) {
	for _, text := range []string{
		"qmut: mutant broken\n",
		"qmut: mutant 1 qenv/a.go:1 f: drop call (a())\n",
		"qmut: mutant 0 qenv/a.go:1 f: drop call (a())\nqmut: mutant 0 qenv/b.go:1 f: drop call (b())\n",
	} {
		if _, err := inventory(text); err == nil {
			t.Fatalf("accepted %q", text)
		}
	}
	// The descriptor can itself contain a colon, line number, and spaces.
	m := mustInventory(t, "qmut: mutant 0 qenv/a.go:1 f: drop call (log(\"a:99 b\"))\n")
	if m[0].file != "qenv/a.go" {
		t.Fatalf("descriptor parsed as path: %v", m)
	}
}

func TestDiscoveryPreservesCWDAndDuplicateBasenames(t *testing.T) {
	input := `{"ImportPath":"m/root","Dir":"/root","TestGoFiles":["root_test.go"]}
{"ImportPath":"m/a/choice","Dir":"/a/choice","TestGoFiles":["a_test.go"]}
{"ImportPath":"m/b/choice","Dir":"/b/choice","XTestGoFiles":["b_test.go"]}
{"ImportPath":"m/untested","Dir":"/untested"}`
	suites, err := testSuites(strings.NewReader(input), "/work")
	if err != nil || len(suites) != 3 {
		t.Fatalf("%v %v", suites, err)
	}
	seen := map[string]bool{}
	for _, s := range suites {
		if seen[s.binary] {
			t.Fatalf("binary overwritten: %s", s.binary)
		}
		seen[s.binary] = true
	}
	if suites[1].dir != "/a/choice" || suites[2].dir != "/b/choice" {
		t.Fatal("lost package directories")
	}
	for _, input := range []string{"", `{"ImportPath":"m/a","TestGoFiles":["a_test.go"]}`, "{"} {
		if _, err := testSuites(strings.NewReader(input), "/work"); err == nil {
			t.Fatalf("admitted malformed package list %q", input)
		}
	}
}

func TestExecutionUsesSuiteCWDAndLocalActivation(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "binary with spaces")
	script := fmt.Sprintf(`#!/bin/sh
[ "$PWD" = %q ] || exit 17
case "$GOTOGETHER_MUTANT" in
 list) echo 'qmut: mutant 0 qenv/a.go:1 f: drop call (a())'; exit 0;;
 1) exit 0;;
 0) exit 1;;
 *) exit 18;;
esac
`, dir)
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s := suite{pkg: "fake", dir: dir, binary: binary}
	output, passed, err := execute(t.Context(), s, "list", 3072*1024, inventoryMode)
	if err != nil || !passed || !strings.HasPrefix(output, "qmut: mutant 0 ") {
		t.Fatalf("inventory: %q %v %v", output, passed, err)
	}
	_, passed, err = execute(t.Context(), s, "1", 3072*1024, mutantMode)
	if err != nil || !passed {
		t.Fatalf("baseline failed: %v %v", passed, err)
	}
	_, passed, err = execute(t.Context(), s, "0", 3072*1024, mutantMode)
	if err != nil || passed {
		t.Fatalf("failed mutant survived: %v %v", passed, err)
	}
	s.binary = filepath.Join(dir, "absent")
	if _, _, err := execute(t.Context(), s, "0", 3072*1024, mutantMode); err == nil {
		t.Fatal("missing binary treated as killed mutant")
	}
	boom := errors.New("infrastructure failure")
	if _, err := survives(candidate{targets: []target{{0, 0}}}, func(target) (bool, error) { return false, boom }); !errors.Is(err, boom) {
		t.Fatal("lost execution failure")
	}
}

func TestBaselineFailureIncludesDiagnostics(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "bad-baseline")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 'specific assertion failed' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	err := baseline(t.Context(), suite{dir: dir, binary: binary}, 3072*1024)
	if err == nil || !strings.Contains(err.Error(), "specific assertion failed") {
		t.Fatalf("lost baseline diagnostics: %v", err)
	}
}
