package docsnippets

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// backticked is a toolkit name in a doc's prose, `pkg.Ident` (inside a
// longer span too: `grist.Upcast(1, ...)`).
var backticked = regexp.MustCompile("`([a-z][a-z0-9]*)\\.([A-Z][A-Za-z0-9_]*)")

// notBuilt is a design note's explicit marker for names it designs and
// the code does not have yet: <!-- not built: grist.Within grist.Also -->.
var notBuilt = regexp.MustCompile(`<!--\s*not built:([^>]*)-->`)

// records are the docs that record what was, in the order it was decided,
// and may name what is gone or was renamed since: their names are not
// checked (fenced go blocks of DESIGN still are, by TestSnippets).
var records = map[string]bool{
	"HISTORY.md": true, "ROADMAP.md": true, "CHANGELOG.md": true, "DESIGN.md": true,
	"refactors.md": true, "performance.md": true,
	"TRIAL.md": true, // a newcomer's friction log, as it happened
}

// TestProseNames: every backticked `pkg.Ident` of a doc's prose, pkg one
// of the toolkit's packages, is a name that package exports, or the doc
// says it is not built (<!-- not built: pkg.Ident ... -->). A design
// note that taught grist.UpcastFrom and grist.Reindex, neither of which
// was ever built, read as a guide (review 11); fenced blocks were
// checked, prose was not. Methods and fields (`Binding.StepsToITF`,
// `cfg.Seeds`) are not package-qualified and not checked. A marker naming
// what is exported fails too: the name was built, and the note should
// say how.
func TestProseNames(t *testing.T) {
	apis := map[string]map[string]bool{}
	checked := 0
	for _, doc := range docs(t) {
		if records[filepath.Base(doc)] {
			continue
		}
		b, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, doc)
		text := string(b)
		designed := map[string]bool{}
		for _, m := range notBuilt.FindAllStringSubmatch(text, -1) {
			for _, n := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ' ' || r == ',' || r == '\n' }) {
				designed[n] = true
			}
		}
		// fenced blocks are TestSnippets': prose only here
		text = fencedAny.ReplaceAllStringFunc(text, func(s string) string { return strings.Repeat("\n", strings.Count(s, "\n")) })
		var missing []string
		for i, line := range strings.Split(text, "\n") {
			for _, m := range backticked.FindAllStringSubmatch(line, -1) {
				if packages[m[1]] == "" {
					continue
				}
				if apis[m[1]] == nil {
					apis[m[1]] = exported(t, packages[m[1]])
				}
				checked++
				name := m[1] + "." + m[2]
				if !apis[m[1]][m[2]] && !designed[name] {
					missing = append(missing, fmt.Sprintf("%s:%d: `%s`", rel, i+1, name))
				}
			}
		}
		if len(missing) > 0 {
			t.Errorf("names their packages do not export (renamed or removed? a design not built yet says so in the doc: <!-- not built: pkg.Name ... -->):\n  %s",
				strings.Join(missing, "\n  "))
		}
		var stale []string
		for n := range designed {
			pkg, id, ok := strings.Cut(n, ".")
			if !ok || packages[pkg] == "" {
				stale = append(stale, n+" (no toolkit package)")
				continue
			}
			if apis[pkg] == nil {
				apis[pkg] = exported(t, packages[pkg])
			}
			if apis[pkg][id] {
				stale = append(stale, n+" (exported now: say how it was built, and drop it from the marker)")
			}
		}
		if len(stale) > 0 {
			sort.Strings(stale)
			t.Errorf("%s: a <!-- not built: --> marker names %s", rel, strings.Join(stale, ", "))
		}
	}
	if checked < 200 {
		t.Fatalf("only %d backticked names checked under %s: is the walk wrong?", checked, root)
	}
}

// fencedAny is any fenced block (go, sh, yaml, quint...).
var fencedAny = regexp.MustCompile("(?s)```[a-z]*\n.*?```")
