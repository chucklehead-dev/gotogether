package gristtest

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/internal/ruledrop"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// MutantEnv is the environment variable naming the mutant a test binary
// runs as: Mutants sets it for each mutant's binary.
const MutantEnv = "GRIST_MUTANT"

// Mutant is the mutant this test binary was built with ("" for the app
// as it is): what a TestMutant switches on, and what qtest.System's
// Mutant field takes. A rule's generated mutant (Mutants.App) is
// "drop-<rule>": the app as it is, with the rule dropped (DroppedRule).
func Mutant() string { return os.Getenv(MutantEnv) }

// dropPrefix starts the name of a rule's generated mutant.
const dropPrefix = "drop-"

// a rule's generated mutant drops it in this binary, before any rule
// runs: grist skips the rule ruledrop names (only test binaries import
// gristtest, so no other binary can)
func init() {
	if r, ok := strings.CutPrefix(Mutant(), dropPrefix); ok {
		ruledrop.Dropped = r
	}
}

// DroppedRule says whether this binary is a rule's generated mutant
// (Mutants.App: the app as it is, the rule dropped), naming the rule, and
// the claim that catches it: some run rejects a command it guards, which
// committed where the model's guard (the rule's hole) refuses it. A
// TestMutant explores, before its switch, a workload where the rule
// refuses, and expects caught:
//
//	if rule, caught, ok := gristtest.DroppedRule(App); ok {
//		_ = rule // the workload that reaches its refusal
//		/* ... */
//		return
//	}
//
// A rule that guards no command (its event only a processor's) panics:
// no generated mutant is made of it.
func DroppedRule(app *grist.App) (rule string, caught qtest.Claim, ok bool) {
	name, ok := strings.CutPrefix(Mutant(), dropPrefix)
	if !ok {
		return "", nil, false
	}
	for _, r := range app.Catalog().Rules {
		if r.Name != name {
			continue
		}
		var es []qtest.Expectation
		for _, g := range r.Guards {
			q, _ := grist.QuintName(g)
			es = append(es, qtest.Rejects(q))
		}
		switch len(es) {
		case 0:
			panic(fmt.Sprintf("gristtest.DroppedRule: rule %s guards no command (none declares %s in grist.Appends): it has no generated mutant", name, r.On))
		case 1:
			return name, qtest.Some(es[0]), true
		}
		return name, qtest.Some(qtest.AnyOf(es...)), true
	}
	panic(fmt.Sprintf("gristtest.DroppedRule: this binary is mutant %s, but %s has no rule %s", Mutant(), app.Name, name))
}

// ruleMutants are app's generated mutants, one per rule that guards a
// command: "drop-<rule>".
func ruleMutants(app *grist.App) []string {
	if app == nil {
		return nil
	}
	var out []string
	for _, r := range app.Catalog().Rules {
		if len(r.Guards) > 0 {
			out = append(out, dropPrefix+r.Name)
		}
	}
	return out
}

// Mutants are an application's mutants, each a directory of Dir whose
// .go files replace the package's files of the same names (a go build
// overlay: the app's own files stay as they are, each mutant a whole
// file to diff against them).
type Mutants struct {
	// Names are the mutants, each a directory of Dir.
	Names []string
	// FullOnly are those built and run in full mode only (not under
	// -short): a mutant whose claim repeats a mechanism a per-push test
	// checks, building its binary being most of its cost.
	FullOnly []string
	// Dir is where they are (default testdata/mutants).
	Dir string
	// Test is the test each mutant's binary runs (default TestMutant),
	// switching on Mutant().
	Test string
	// App, when set, adds a generated mutant per rule of its catalog that
	// guards a command (grist.NewRule): "drop-<rule>", the app as it is
	// with the rule dropped, built without an overlay; its TestMutant
	// explores where the rule refuses and expects DroppedRule's claim.
	App *grist.App
}

// Run builds the package's tests once per mutant with its overlay and
// runs Test in each (in parallel, -short and the deadline passed on),
// failing with the binary's output when it fails and logging its
// summaries. In a mutant's own binary it skips; a mutant with no files,
// or a file that replaces none of the package's, fails it (overlay).
func (m Mutants) Run(t *testing.T) {
	if Mutant() != "" {
		t.Skip("already a mutant's binary")
	}
	dir, test := m.Dir, m.Test
	if dir == "" {
		dir = filepath.Join("testdata", "mutants")
	}
	if test == "" {
		test = "TestMutant"
	}
	generated := ruleMutants(m.App)
	for _, name := range m.Names {
		if strings.HasPrefix(name, dropPrefix) {
			t.Fatalf("mutant %s: names starting with %s are the generated mutants' (a rule dropped, Mutants.App): rename it", name, dropPrefix)
		}
	}
	for _, name := range append(slices.Clone(m.Names), generated...) {
		t.Run(name, func(t *testing.T) {
			if testing.Short() && slices.Contains(m.FullOnly, name) {
				t.Skip("full mode: its claim repeats a per-push test's mechanism")
			}
			t.Parallel()
			args := []string{"test", "-count=1"}
			if !slices.Contains(generated, name) {
				args = append(args, "-overlay", overlay(t, dir, name))
			}
			args = append(args, "-run", "^"+test+"$", "-v")
			if testing.Short() {
				args = append(args, "-short")
			}
			if d, ok := t.Deadline(); ok {
				args = append(args, "-timeout", time.Until(d).Round(time.Second).String()) //qenv:ok the test binary's own deadline, handed to the mutant's
			}
			cmd := exec.Command("go", append(args, ".")...)
			// a mutant's claims have their stated miss chance at their own
			// seeds: a nightly's GOTOGETHER_SEEDS would only make it slow
			for _, e := range os.Environ() {
				if !strings.HasPrefix(e, "GOTOGETHER_SEEDS=") && !strings.HasPrefix(e, MutantEnv+"=") {
					cmd.Env = append(cmd.Env, e)
				}
			}
			cmd.Env = append(cmd.Env, MutantEnv+"="+name)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("mutant %s: %v\n%s", name, err, out)
			}
			ran := false
			for _, l := range strings.Split(string(out), "\n") {
				if strings.Contains(l, "runs, ") || strings.HasPrefix(strings.TrimSpace(l), "---") {
					t.Log(strings.TrimSpace(l))
				}
				ran = ran || strings.HasPrefix(strings.TrimSpace(l), "--- PASS: "+test)
			}
			if !ran {
				t.Fatalf("mutant %s: %s did not run (no --- PASS line): its binary ran nothing\n%s", name, test, out)
			}
		})
	}
}

// overlay writes the go build overlay of mutant name (dir/name's .go
// files in place of the package's of the same names) into a temporary
// directory and returns its path; a mutant with no files, or a file that
// replaces none of the package's, fails t.
func overlay(t testing.TB, dir, name string) string {
	t.Helper()
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(here, dir, name, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("mutant %s has no files in %s", name, filepath.Join(dir, name))
	}
	replace := map[string]string{}
	for _, f := range files {
		target := filepath.Join(here, filepath.Base(f))
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("%s replaces no file of the package: %v", filepath.Join(dir, name, filepath.Base(f)), err)
		}
		replace[target] = f
	}
	b, _ := json.Marshal(map[string]any{"Replace": replace})
	path := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
