package qtest

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modelOptionsFixture gives both the binding and simulate section constants:
// the default effective MAX is 2, and a test-local MAX=1 must override both.
func modelOptionsFixture(t *testing.T, search string) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	spec := []byte(`module modeloptions {
  const MAX: int
  var n: int
  action init = n' = 0
  action inc = all { n < MAX, n' = n + 1 }
  action reset = n' = 0
  action step = any { inc, reset }
  val nonnegative = n >= 0
  val small = n <= 1
}
`)
	binding := []byte(`format: gotogether.binding/2
spec: options.qnt
module: modeloptions
constants: {MAX: "3"}
actions: {inc: {}, reset: {}}
invariants: [nonnegative, small]
simulate:
  constants: {MAX: "2"}
  steps: 4
  samples: 2
` + search)
	paths := map[string][]byte{filepath.Join(dir, "options.qnt"): spec, filepath.Join(dir, "options.yaml"): binding}
	for p, b := range paths {
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "options.yaml"), func() {
		t.Helper()
		for p, want := range paths {
			got, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("model checks rewrote authored source %s", p)
			}
		}
	}
}

func configuredModelExpect(t *testing.T, m *ModelCheck, claims ...ModelClaim) *failTB {
	t.Helper()
	f := &failTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends this test goroutine.
		defer close(done)
		m.Expect(f, claims...)
	}()
	<-done
	return f
}

func TestModelOptionsBounds(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	binding, unchanged := modelOptionsFixture(t, "")
	defer unchanged()
	base := Model(t, binding)
	for _, tc := range []struct {
		name    string
		options ModelOptions
		want    string
		failed  bool
	}{
		{"complete", ModelOptions{}, "whole state graph", false},
		{"depth proof", ModelOptions{Depth: 1}, "within 1 steps of init", false},
		{"state truncation", ModelOptions{States: 1}, "truncated by the state bound", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := configuredModelExpect(t, base.WithOptions(tc.options), Proves("nonnegative"))
			output := strings.Join(append(f.logs, f.errs...), "\n")
			if f.failed != tc.failed || !strings.Contains(output, tc.want) {
				t.Fatalf("failed=%v want%v; output:\n%s", f.failed, tc.failed, output)
			}
			if tc.failed && !strings.Contains(output, "no verdict") {
				t.Fatalf("state truncation was treated as a proof or counterexample: %s", output)
			}
		})
	}
}

func TestModelOptionsConstantsAreLocalAndCopied(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	binding, unchanged := modelOptionsFixture(t, "")
	defer unchanged()
	base := Model(t, binding)
	input := map[string]string{"MAX": "1"}
	small := base.WithOptions(ModelOptions{Constants: input})
	input["MAX"] = "2"
	other := base.WithOptions(ModelOptions{Constants: map[string]string{"MAX": "2"}})
	for _, tc := range []struct {
		name  string
		check *ModelCheck
		claim ModelClaim
		want  string
	}{
		{"copied caller constants", small, Proves("small"), "whole state graph"},
		{"sibling constants", other, Refutes("small").InAtMost(2), "replayed by quint test"},
		{"unchanged base", base, Refutes("small").InAtMost(2), "replayed by quint test"},
		{"replacement not merge", small.WithOptions(ModelOptions{Depth: 3}), Refutes("small").InAtMost(2), "replayed by quint test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := configuredModelExpect(t, tc.check, tc.claim)
			if f.failed || !strings.Contains(strings.Join(f.logs, "\n"), tc.want) {
				t.Fatalf("constant selection/copy changed native claim: errors=%s logs=%s", f.errs, f.logs)
			}
		})
	}
}

func TestModelOptionsInvalidRequestsPrecedeBindingRead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options ModelOptions
	}{
		{"negative states", ModelOptions{States: -1}},
		{"negative depth", ModelOptions{Depth: -1}},
		{"blank name", ModelOptions{Constants: map[string]string{"": "1"}}},
		{"padded name", ModelOptions{Constants: map[string]string{" MAX": "1"}}},
		{"embedded whitespace", ModelOptions{Constants: map[string]string{"M AX": "1"}}},
		{"assignment as name", ModelOptions{Constants: map[string]string{"MAX=1": "2"}}},
		{"empty expression", ModelOptions{Constants: map[string]string{"MAX": ""}}},
		{"blank expression", ModelOptions{Constants: map[string]string{"MAX": " \t"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No Quint or input exists: request validation must run before model IO.
			missing := filepath.Join(t.TempDir(), "missing.yaml")
			f := configuredModelExpect(t, (&ModelCheck{binding: missing}).WithOptions(tc.options), Proves("small"))
			text := strings.Join(f.errs, "\n")
			if !f.failed || text == "" || strings.Contains(text, "no such file") || strings.Contains(text, "cannot find") {
				t.Fatalf("invalid request reached binding IO: %s", text)
			}
		})
	}
}

func TestModelOptionsInvalidConstantsAreNotRefutations(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	binding, unchanged := modelOptionsFixture(t, "")
	defer unchanged()
	for _, tc := range []struct {
		name      string
		constants map[string]string
	}{
		{"unknown name", map[string]string{"MISSING": "2"}},
		{"invalid expression", map[string]string{"MAX": "("}},
		{"wrong type", map[string]string{"MAX": "true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := configuredModelExpect(t, Model(t, binding).WithOptions(ModelOptions{Constants: tc.constants}), Refutes("small"))
			if !f.failed || len(f.errs) == 0 || len(f.logs) != 0 {
				t.Fatalf("invalid constant became a model refutation: errors=%s logs=%s", f.errs, f.logs)
			}
		})
	}
}

func TestModelOptionsZeroBoundsInheritBinding(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	for _, tc := range []struct {
		name, search, want string
		options            ModelOptions
		failed             bool
	}{
		{"binding depth", "  search: {depth: 1}\n", "within 1 steps of init", ModelOptions{}, false},
		{"binding states", "  search: {states: 1}\n", "no verdict", ModelOptions{}, true},
		{"override binding states", "  search: {states: 1}\n", "whole state graph", ModelOptions{States: 100}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding, unchanged := modelOptionsFixture(t, tc.search)
			defer unchanged()
			f := configuredModelExpect(t, Model(t, binding).WithOptions(tc.options), Proves("nonnegative"))
			output := strings.Join(f.logs, "\n") + "\n" + strings.Join(f.errs, "\n")
			if f.failed != tc.failed || !strings.Contains(output, tc.want) {
				t.Fatalf("binding/test bound precedence: errors=%s logs=%s", f.errs, f.logs)
			}
		})
	}
}
