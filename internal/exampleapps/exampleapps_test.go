// Package exampleapps checks AGENTS.md's rule for an example app against
// every example directory: a README with the table of its tests, a row
// in examples/README.md's tracks and its name in the README's, a model,
// a run-demo.sh in the conventions examples/README.md states, and a
// mutant. Four examples once had no run-demo.sh and nothing said so
// (review 11).
package exampleapps

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// root is the repository's root, from this package's directory.
const root = "../.."

// exempt are the examples a part of the rule does not apply to, each
// with its reason. Read-only.
var exempt = map[string]map[string]string{
	"sktrace": {
		"all": "a library (SimKube traces as load shapes) that fleetrollout and retrystorm use: no system to model, demo or mutate",
	},
	"gosim": {
		"model": "its runs are judged by the models of glomers' broadcast, checkout and edgepublish, which it imports",
	},
}

// examples are the module directories under examples/.
func examples(t *testing.T) []string {
	dirs, err := filepath.Glob(filepath.Join(root, "examples", "*", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range dirs {
		out = append(out, filepath.Base(filepath.Dir(d)))
	}
	if len(out) < 20 {
		t.Fatalf("only %d examples under %s/examples: is the walk wrong?", len(out), root)
	}
	return out
}

var (
	testRow   = regexp.MustCompile("(?m)^\\|.*`([a-z]+\\.)?Test[A-Z0-9]\\w*")
	shebang   = "#!/usr/bin/env bash\n"
	setFlags  = regexp.MustCompile(`(?m)^set -e?uo pipefail$`)
	cdSelf    = `cd "$(dirname "$0")"`
	exitUsage = regexp.MustCompile(`exit 2\b`)
	mutantIn  = regexp.MustCompile(`(?i)\bmutant`)
)

func TestExampleApps(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			return ""
		}
		return string(b)
	}
	index := read(filepath.Join(root, "examples", "README.md"))
	front := read(filepath.Join(root, "README.md"))
	for _, name := range examples(t) {
		skip := exempt[name]
		if skip["all"] != "" {
			continue
		}
		dir := filepath.Join(root, "examples", name)
		readme := read(filepath.Join(dir, "README.md"))
		switch {
		case readme == "":
			t.Errorf("examples/%s: no README.md (an example app's README has the table of its tests)", name)
		case !testRow.MatchString(readme):
			t.Errorf("examples/%s/README.md: no table naming its tests (a row `| `TestX` | ... | what it catches |`)", name)
		}
		if !strings.Contains(index, "[`"+name+"`]("+name+")") {
			t.Errorf("examples/%s: no row in examples/README.md (add it to its track's table)", name)
		}
		if !strings.Contains(front, "examples/"+name) {
			t.Errorf("examples/%s: not in the README's example tracks (name it in its track's line)", name)
		}
		if skip["model"] == "" {
			qnt, _ := filepath.Glob(filepath.Join(dir, "model", "*.qnt"))
			if len(qnt) == 0 {
				t.Errorf("examples/%s: no model/*.qnt (an example app's model is in model/)", name)
			}
		}
		demo := filepath.Join(dir, "run-demo.sh")
		fi, err := os.Stat(demo)
		if err != nil {
			t.Errorf("examples/%s: no run-demo.sh (examples/README.md, \"run-demo.sh\", says what it does)", name)
			continue
		}
		if fi.Mode().Perm()&0o111 == 0 {
			t.Errorf("examples/%s/run-demo.sh is not executable (chmod +x)", name)
		}
		src := read(demo)
		header := comments(src)
		for _, c := range []struct {
			ok   bool
			what string
		}{
			{strings.HasPrefix(src, shebang), "begin with #!/usr/bin/env bash"},
			{setFlags.MatchString(src), "set -euo pipefail (or -uo, each step's status checked)"},
			{strings.Contains(src, cdSelf), `cd "$(dirname "$0")" before anything else`},
			{strings.Contains(header, "MUTANT="), "name MUTANT= and its mutants in its header comment"},
			{exitUsage.MatchString(src) || strings.Contains(src, "exec "), "exit 2 for what it cannot run (an unknown MUTANT=)"},
		} {
			if !c.ok {
				t.Errorf("examples/%s/run-demo.sh does not %s (examples/README.md, \"run-demo.sh\")", name, c.what)
			}
		}
		if !mutantIn.MatchString(readme) {
			t.Errorf("examples/%s/README.md names no mutant (an example app has at least one, and its README says what catches it)", name)
		}
	}
}

// comments is a script's leading comment block, the shebang aside.
func comments(src string) string {
	var b strings.Builder
	for i, line := range strings.Split(src, "\n") {
		if i == 0 && strings.HasPrefix(line, "#!") {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// The exemptions name examples that exist, and a test of the rule fails
// without them: an exemption outliving its example would hide nothing,
// one hiding a part that is there would be stale.
func TestExemptionsStillNeeded(t *testing.T) {
	have := map[string]bool{}
	for _, n := range examples(t) {
		have[n] = true
	}
	for name, parts := range exempt {
		if !have[name] {
			t.Errorf("exempt names examples/%s, which is no example: drop it", name)
			continue
		}
		dir := filepath.Join(root, "examples", name)
		if parts["model"] != "" {
			if qnt, _ := filepath.Glob(filepath.Join(dir, "model", "*.qnt")); len(qnt) > 0 {
				t.Errorf("examples/%s has a model/ now: drop its exemption", name)
			}
		}
		if parts["all"] != "" {
			if _, err := os.Stat(filepath.Join(dir, "run-demo.sh")); err == nil {
				t.Errorf("examples/%s has a run-demo.sh now: drop its exemption, and check it as an app", name)
			}
		}
	}
}
