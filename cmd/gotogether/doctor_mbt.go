package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/mbtmodel"
)

// A specUse is a spec (and main module) a connect test of the module
// names, as doctor finds it: the spec's path, from the module's directory.
type specUse struct{ spec, main string }

// mbtPieces are the module's compiled models for connect (gotogether
// compile -spec; connect.CompiledFor), by spec and main module: those its
// connect tests name, each current, stale (a skew with the fix) or none
// (its tests need quint, with the compile that writes one), and every
// artifact found that no test names, which is reported as connect would
// read it: current, stale, an orphan whose spec is not here, one kept where
// connect does not look for it, or a plain one an earlier gotogether kept
// (connect finds only the gzipped form). The check is LoadMBT's, by hashes,
// as a run without quint makes it; gotogether compile -spec -check compares
// a compile now by meaning, where quint is. named are -compiled files,
// reported with the others where they are connect's.
func mbtPieces(dir string, named []string) []piece {
	uses, artifacts := findConnectUses(dir)
	for _, n := range named {
		if isMBTName(n) {
			artifacts = append(artifacts, n)
		}
	}
	seen := map[string]bool{}
	var ps []piece
	for _, u := range uses {
		path := mbtmodel.CompiledFor(u.spec, u.main)
		seen[absPath(path)] = true
		p := piece{name: "spec " + u.spec + mbtmodel.MainFlag(u.main)}
		if _, err := os.Stat(path); err != nil {
			p.version = "none"
			p.note = "its connect tests need quint: gotogether compile -spec " + u.spec + mbtmodel.MainFlag(u.main) + ", where quint is, and check it in"
			if plain := mbtmodel.PlainBeside(path); plain != "" {
				seen[absPath(plain)] = true
				p.version, p.note = "plain", plain
				p.skew = plainSkew(plain, path, u.spec, u.main)
			}
			ps = append(ps, p)
			continue
		}
		ps = append(ps, mbtState(p, path, u.spec, u.main))
	}
	slices.Sort(artifacts)
	for _, a := range slices.Compact(artifacts) {
		if seen[absPath(a)] {
			continue
		}
		seen[absPath(a)] = true
		cm, _, err := mbtmodel.Load(a)
		if err != nil && cm == nil {
			// unreadable or stale: read what it names for the report
			cm = peekMBT(a)
		}
		if cm == nil {
			ps = append(ps, piece{name: a, version: "unreadable", note: a, skew: err.Error()})
			continue
		}
		spec := filepath.Join(filepath.Dir(a), filepath.FromSlash(cm.Spec))
		p := piece{name: "spec " + spec + mbtmodel.MainFlag(cm.Main)}
		want := mbtmodel.CompiledFor(spec, cm.Main)
		switch _, specErr := os.Stat(spec); {
		case specErr != nil:
			p.version, p.note = "orphan", a
			p.skew = "its spec " + spec + " is not here, so no connect test reads " + a + ": remove it, or compile the spec it belongs to beside that spec"
		case !strings.HasSuffix(a, ".gz") && absPath(a) == absPath(strings.TrimSuffix(want, ".gz")):
			p.version, p.note = "plain", a
			p.skew = plainSkew(a, want, spec, cm.Main)
		case absPath(a) != absPath(want):
			p.version, p.note = "misplaced", a
			p.skew = "connect looks for " + spec + mbtmodel.MainFlag(cm.Main) + "'s compiled model at " + want + ", not " + a + ": compile it there (gotogether compile -spec " + spec + mbtmodel.MainFlag(cm.Main) + ") and remove " + a
		default:
			p = mbtState(p, a, spec, cm.Main)
		}
		ps = append(ps, p)
	}
	return ps
}

// mbtState is p for the compiled model at path of spec's main: current,
// or stale with the fix.
func mbtState(p piece, path, spec, main string) piece {
	p.version = "current"
	p.note = path + "; by hashes: gotogether compile -spec " + spec + mbtmodel.MainFlag(main) + " -check compares its meaning, where quint is"
	if _, _, err := mbtmodel.Load(path); err != nil {
		p.version, p.note, p.skew = "stale", path, err.Error()
	}
	return p
}

// plainSkew is the fix for a plain artifact an earlier gotogether kept
// where connect now reads the gzipped one (want).
func plainSkew(plain, want, spec, main string) string {
	return plain + " is the plain form an earlier gotogether kept, which connect no longer finds (it reads " + filepath.Base(want) +
		"): compile it again (gotogether compile -spec " + spec + mbtmodel.MainFlag(main) + ") and remove " + plain
}

// peekMBT is what an artifact Load refused says it is of (its spec and
// main), nil when it says nothing readable.
func peekMBT(path string) *mbtmodel.Model {
	cm, err := mbtmodel.Peek(path)
	if err != nil || cm.Spec == "" {
		return nil
	}
	return cm
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// isMBTName reports whether path is named as a compiled model for connect
// (gotogether.mbt/1), gzipped or plain.
func isMBTName(path string) bool {
	return strings.HasSuffix(path, mbtmodel.Ext) || strings.HasSuffix(path, strings.TrimSuffix(mbtmodel.Ext, ".gz"))
}

// connectSpec finds a connect.Config's spec in a test's source, a literal
// Spec: "x.qnt", and its Main: "m" in the same composite literal (before
// its first closing brace).
var (
	connectSpec = regexp.MustCompile(`\bSpec:\s*"([^"]+\.qnt)"`)                         // read-only
	connectMain = regexp.MustCompile(`\bMain:\s*"([^"]+)"`)                              // read-only
	connectPkg  = regexp.MustCompile(`"github\.com/chucklehead-dev/gotogether/connect"`) // read-only
)

// findConnectUses are the specs (with their main modules) the module's
// connect tests name, read from the literal Config{Spec: ..., Main: ...}
// of test files that import connect, each existing spec once, sorted; and
// the compiled models for connect found in the module. Both walk as
// findBindings does (outside testdata, hidden directories, node_modules
// and nested modules). A spec named through a variable is not seen; its
// artifact, if any, is still listed.
func findConnectUses(dir string) (uses []specUse, artifacts []string) {
	set := map[specUse]bool{}
	walkModule(dir, func(path string) {
		if isMBTName(path) {
			artifacts = append(artifacts, path)
			return
		}
		if !strings.HasSuffix(path, "_test.go") {
			return
		}
		src, err := os.ReadFile(path)
		if err != nil || !connectPkg.Match(src) {
			return
		}
		for _, m := range connectSpec.FindAllSubmatchIndex(src, -1) {
			spec := filepath.Join(filepath.Dir(path), filepath.FromSlash(string(src[m[2]:m[3]])))
			if _, err := os.Stat(spec); err != nil {
				continue
			}
			rest := src[m[1]:]
			if i := strings.IndexByte(string(rest), '}'); i >= 0 {
				rest = rest[:i]
			}
			// the literal's Main may come before its Spec
			lit := src[strings.LastIndexAny(string(src[:m[0]]), "{\n")+1 : m[0]]
			main := ""
			for _, part := range [][]byte{lit, rest} {
				if mm := connectMain.FindSubmatch(part); mm != nil {
					main = string(mm[1])
				}
			}
			set[specUse{filepath.Clean(spec), main}] = true
		}
	})
	for u := range set {
		uses = append(uses, u)
	}
	slices.SortFunc(uses, func(a, b specUse) int {
		if c := strings.Compare(a.spec, b.spec); c != 0 {
			return c
		}
		return strings.Compare(a.main, b.main)
	})
	return uses, artifacts
}
