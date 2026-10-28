package ciconfig

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const core = "github.com/chucklehead-dev/gotogether"

// reRequire is a go.mod's requirement of the core or one of its modules,
// and rePseudo a pseudo-version's commit (read-only).
var (
	reRequire = regexp.MustCompile(`(?m)^\s*(?:require\s+)?(` + regexp.QuoteMeta(core) + `(?:/\S+)?) (v\S+)`)
	rePseudo  = regexp.MustCompile(`-([0-9a-f]{12})$`)
	reModules = regexp.MustCompile(`(?m)^modules=\(([^)]*)\)`)
)

// published lists the published modules: every go.mod outside the
// examples, ci/ (the consumer is a user's), testdata and internal/ with no replace directive, the core
// aside. A published module cannot have one: go install refuses it.
func published(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("../..", p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() && (rel == "examples" || rel == "internal" || rel == "ci" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) && rel != "." {
			return filepath.SkipDir
		}
		if d.Name() != "go.mod" || rel == "go.mod" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !regexp.MustCompile(`(?m)^replace\b`).Match(b) {
			out = append(out, filepath.Dir(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// Every published module requires the core, and any published module it
// requires, at one commit: a user's go get resolves the newest of their
// pins, a combination no job built while they differed (the eleventh
// review found seven). None may lag: a module the newer core breaks is
// fixed with the bump, since a user gets that core with it anyway. Move
// them with ci/bump-core.sh <pushed commit>, whose list of modules is
// this one.
func TestCorePinsAgree(t *testing.T) {
	mods := published(t)
	script, err := os.ReadFile("../../ci/bump-core.sh")
	if err != nil {
		t.Fatal(err)
	}
	m := reModules.FindSubmatch(script)
	if m == nil {
		t.Fatal("ci/bump-core.sh lists no modules=(...)")
	}
	listed := strings.Fields(string(m[1]))
	slices.Sort(listed)
	if !slices.Equal(listed, mods) {
		t.Errorf("ci/bump-core.sh moves %v, the published modules (go.mod without replace) are %v: list each in it", listed, mods)
	}
	pins := map[string][]string{} // commit -> "module requires path"
	for _, mod := range mods {
		b, err := os.ReadFile(filepath.Join("../..", mod, "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range reRequire.FindAllSubmatch(b, -1) {
			path, v := string(r[1]), string(r[2])
			c := rePseudo.FindStringSubmatch(v)
			key := v
			if c != nil {
				key = c[1]
			}
			pins[key] = append(pins[key], mod+" requires "+path+" "+v)
		}
	}
	if len(pins) > 1 {
		var lines []string
		for _, ps := range pins {
			lines = append(lines, ps...)
		}
		slices.Sort(lines)
		t.Errorf("the published modules pin the core at %d commits; move them to one (ci/bump-core.sh <pushed commit>):\n  %s", len(pins), strings.Join(lines, "\n  "))
	}
}
