package main

import (
	"slices"
	"testing"
)

func TestOwnerSuiteRunsFirstWithItsLocalID(t *testing.T) {
	root := suite{pkg: coreModule + "/qenv", mutants: mustInventory(t, `qmut: mutant 0 qenv/root.go:1 f: drop call (f())
qmut: mutant 1 qenv/internal/choice/choice.go:258 checked.of: drop call (r.check())
`)}
	owner := suite{pkg: coreModule + "/qenv/internal/choice", mutants: mustInventory(t, `qmut: mutant 0 qenv/internal/choice/choice.go:258 checked.of: drop call (r.check())
`)}
	baselines := []string{}
	candidates, err := admit([]suite{root, owner}, func(s suite) error { baselines = append(baselines, s.pkg); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(baselines, []string{root.pkg, owner.pkg}) {
		t.Fatalf("baseline admission changed: %v", baselines)
	}
	var c candidate
	for _, candidate := range candidates {
		if candidate.file == "qenv/internal/choice/choice.go" {
			c = candidate
		}
	}
	if !slices.Equal(c.targets, []target{{1, 0}, {0, 1}}) {
		t.Fatalf("wrong source-local activation order: %v", c.targets)
	}
	t.Run("owner kills before root", func(t *testing.T) {
		var calls []target
		passed, err := survives(c, func(target target) (bool, error) { calls = append(calls, target); return false, nil })
		if err != nil || passed || !slices.Equal(calls, []target{{1, 0}}) {
			t.Fatalf("owner kill did not avoid root: passed=%v calls=%v err=%v", passed, calls, err)
		}
	})
	t.Run("passing owner still needs root", func(t *testing.T) {
		var calls []target
		passed, err := survives(c, func(target target) (bool, error) { calls = append(calls, target); return target.suite == 1, nil })
		if err != nil || passed || !slices.Equal(calls, c.targets) {
			t.Fatalf("owner pass bypassed consumer: passed=%v calls=%v err=%v", passed, calls, err)
		}
	})
	t.Run("all linked suites required to survive", func(t *testing.T) {
		var calls []target
		passed, err := survives(c, func(target target) (bool, error) { calls = append(calls, target); return true, nil })
		if err != nil || !passed || !slices.Equal(calls, c.targets) {
			t.Fatalf("survivor skipped evidence: passed=%v calls=%v err=%v", passed, calls, err)
		}
	})
}

func TestOwnerPreferencePreservesFallbackOrder(t *testing.T) {
	suites := []suite{
		{pkg: coreModule + "/qenv"},
		{pkg: coreModule + "/qenv/qsql"},
		{pkg: coreModule + "/qenv/internal/choice"},
		{pkg: coreModule + "/qenv/simlog"},
	}
	original := []target{{0, 104}, {1, 203}, {2, 35}, {3, 77}}
	for _, tc := range []struct {
		name, file    string
		targets, want []target
	}{
		{"owner in middle", "qenv/internal/choice/choice.go", original, []target{{2, 35}, {0, 104}, {1, 203}, {3, 77}}},
		{"owner already first", "qenv/root.go", original, original},
		{"no owner suite", "qenv/internal/untested/a.go", original, original},
		{"unlinked owner", "qenv/internal/choice/choice.go", []target{{0, 104}, {1, 203}, {3, 77}}, []target{{0, 104}, {1, 203}, {3, 77}}},
		{"parent is not owner", "qenv/internal/choice/child/a.go", original, original},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := candidate{mutant: mutant{source: source{file: tc.file}}, targets: slices.Clone(tc.targets)}
			preferOwner(&c, suites)
			if !slices.Equal(c.targets, tc.want) {
				t.Fatalf("got %v, want %v", c.targets, tc.want)
			}
		})
	}
}
