package validate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
)

// specFiles is the spec at path, hashed, and the files it imports,
// transitively, each hashed once, in the order found: what a verdict on
// the model depends on. An import whose file is missing is quint's to
// report, not the manifest's: it is left out.
func specFiles(path string) (*File, []File, error) {
	spec, err := hashed(path)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{spec.Path: true}
	var imports []File
	todo := []string{spec.Path}
	for len(todo) > 0 {
		p := todo[0]
		todo = todo[1:]
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, imp := range importPaths(string(src)) {
			q := filepath.Join(filepath.Dir(p), filepath.FromSlash(imp))
			if filepath.Ext(q) != ".qnt" {
				q += ".qnt" // quint adds the extension a path leaves out
			}
			if seen[q] {
				continue
			}
			seen[q] = true
			f, err := hashed(q)
			if err != nil {
				continue
			}
			imports = append(imports, *f)
			todo = append(todo, q)
		}
	}
	return spec, imports, nil
}

// importPaths are the files a Quint source imports or re-exports
// (`import M.* from "./m"`, `import M(N = Set("u")) as I from "m.qnt"`,
// either split over lines), in order: the paths quint resolves relative
// to the importing file. It reads tokens, as quint's lexer does, so a
// comment's or a string's `from "x"` imports nothing, and an import
// without `from` (a module of the same file) none. quint's own parse
// would know too, but costs a process (about 0.3 s, as much as the
// typecheck the model cache saves), and Verify and Bundle run where
// quint may be missing.
func importPaths(src string) []string {
	toks := quintTokens(src)
	var paths []string
	for i := 0; i < len(toks); i++ {
		if t := toks[i]; t != "import" && t != "export" {
			continue
		}
		depth := 0 // inside an instance's overrides, anything goes
	scan:
		for j := i + 1; j < len(toks); j++ {
			t := toks[j]
			switch {
			case t == "(":
				depth++
			case t == ")":
				depth--
			case depth > 0:
			case t == "from":
				if j+1 < len(toks) && strings.HasPrefix(toks[j+1], `"`) {
					paths = append(paths, strings.Trim(toks[j+1], `"`))
				}
				i = j
				break scan
			case t == "." || t == "*" || t == "::" || t == "as" || isIdent(t) && !declKeywords[t]:
				// the import's module name, its qualifier, its alias
			default: // the next declaration, or the module's end: no file
				i = j - 1
				break scan
			}
		}
	}
	return paths
}

// declKeywords start a module's declarations (quint's grammar): one
// ends an import that named no file.
// Read-only.
var declKeywords = map[string]bool{
	"const": true, "var": true, "assume": true, "val": true, "pure": true, "type": true, "def": true,
	"action": true, "run": true, "temporal": true, "nondet": true, "import": true, "export": true, "module": true,
}

// isIdent reports whether t is a word (an identifier or a keyword).
func isIdent(t string) bool {
	return t != "" && (t[0] == '_' || 'a' <= t[0] && t[0] <= 'z' || 'A' <= t[0] && t[0] <= 'Z')
}

// quintTokens splits Quint source into the tokens importPaths reads:
// words, strings (quoted, as written: a Quint string has no escapes and
// ends at the next quote), `::`, and every other character on its own.
// Comments (`//` to the line's end, `/* */`) and white space are left
// out.
func quintTokens(src string) []string {
	var toks []string
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case strings.HasPrefix(src[i:], "//"):
			if k := strings.IndexByte(src[i:], '\n'); k >= 0 {
				i += k
			} else {
				i = len(src)
			}
		case strings.HasPrefix(src[i:], "/*"):
			if k := strings.Index(src[i+2:], "*/"); k >= 0 {
				i += 2 + k + 2
			} else {
				i = len(src)
			}
		case c == '"':
			k := strings.IndexByte(src[i+1:], '"')
			if k < 0 {
				return append(toks, src[i:]) // unterminated: quint's to report
			}
			toks = append(toks, src[i:i+1+k+1])
			i += k + 2
		case isIdent(src[i : i+1]):
			j := i + 1
			for j < len(src) && (isIdent(src[j:j+1]) || '0' <= src[j] && src[j] <= '9') {
				j++
			}
			toks = append(toks, src[i:j])
			i = j
		case strings.HasPrefix(src[i:], "::"):
			toks = append(toks, "::")
			i += 2
		default:
			toks = append(toks, src[i:i+1])
			i++
		}
	}
	return toks
}

// verify checks md's model as a replay judges it: the spec the binding
// resolves now (its spec: may be relative to it, and a moved binding's
// another file), or the spec recorded, must be the one hashed, with the
// same imports at the same places beside it.
func (md *RunModel) verify() []error {
	if md.Spec == nil {
		return nil
	}
	spec, how := md.Spec.Path, ""
	if md.Binding != nil {
		b, err := binding.Load(md.Binding.Path)
		if err != nil {
			return nil // the binding missing or unreadable: files() says so
		}
		spec, how = b.SpecPath(), " (as the binding "+md.Binding.Path+" resolves its spec: "+b.Spec+")"
	}
	f, imports, err := specFiles(spec)
	if err != nil {
		return []error{fmt.Errorf("the spec %s%s: %v: replay judges the model the manifest hashed; put it back where the binding finds it (a manifest's directory moves whole, with the model beside the binding as it was)", spec, how, err)}
	}
	var errs []error
	if f.SHA256 != md.Spec.SHA256 {
		errs = append(errs, fmt.Errorf("the spec %s%s is not the one the run was judged on (sha256 %s, the manifest's %s, was %s): replay would judge another model; restore it, or validate anew (gotogether validate, -manifest for a new manifest)",
			spec, how, short(f.SHA256), short(md.Spec.SHA256), md.Spec.Path))
	}
	// imports by their place beside the spec: a model moved whole is the same
	rel := func(root string, fs []File) map[string]string {
		m := map[string]string{}
		for _, i := range fs {
			r, err := filepath.Rel(filepath.Dir(root), i.Path)
			if err != nil {
				r = i.Path
			}
			m[filepath.ToSlash(r)] = i.SHA256
		}
		return m
	}
	was, now := rel(md.Spec.Path, md.Imports), rel(f.Path, imports)
	var names []string
	for r := range was {
		names = append(names, r)
	}
	for r := range now {
		if _, ok := was[r]; !ok {
			names = append(names, r)
		}
	}
	sort.Strings(names)
	for _, r := range names {
		switch h, ok := now[r]; {
		case !ok:
			errs = append(errs, fmt.Errorf("the spec's import %s (beside %s) is missing: replay judges the model the manifest hashed; put it back", r, f.Path))
		case was[r] == "":
			errs = append(errs, fmt.Errorf("the spec %s imports %s, which the run's model did not: replay would judge another model; restore the spec, or validate anew", f.Path, r))
		case h != was[r]:
			errs = append(errs, fmt.Errorf("the spec's import %s (beside %s) changed since the run was judged (sha256 %s, now %s): replay would judge another model; restore it, or validate anew",
				r, f.Path, short(was[r]), short(h)))
		}
	}
	return errs
}

// Bundle copies the files the manifest names into dir, so a manifest
// written there (Write, with dir its directory) travels with them: the
// inputs into dir/inputs, each model's binding, spec and imports into
// dir/model/<n> at their places relative to one another (a binding's
// relative spec: still resolves), and the artifacts into dir/artifacts.
// Its paths become the copies'. A file already under dir stays.
//
// A binding whose spec: is an absolute path keeps naming the original:
// the bundle then holds a copy no replay reads, and Verify on another
// machine refuses the missing spec.
func (m *Manifest) Bundle(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	under := func(p string) bool { return within(dir, p) }
	used := map[string]bool{}
	place := func(sub, base string) string { // a name of its own: two inputs may share a base name
		p := filepath.Join(dir, sub, base)
		for n := 2; used[p]; n++ {
			p = filepath.Join(dir, sub, fmt.Sprintf("%d-%s", n, base))
		}
		used[p] = true
		return p
	}
	for i := range m.Inputs {
		if p := m.Inputs[i].Path; !under(p) {
			to := place("inputs", filepath.Base(p))
			if err := copyFile(p, to); err != nil {
				return err
			}
			m.Inputs[i].Path = to
		}
	}
	for n, md := range m.models() {
		var files []*File
		for _, f := range []*File{md.Binding, md.Spec, md.Convert} {
			if f != nil && !under(f.Path) {
				files = append(files, f)
			}
		}
		for i := range md.Imports {
			if !under(md.Imports[i].Path) {
				files = append(files, &md.Imports[i])
			}
		}
		if len(files) == 0 {
			continue
		}
		root := filepath.Dir(files[0].Path) // their deepest common directory
		for _, f := range files[1:] {
			for !within(root, f.Path) {
				root = filepath.Dir(root)
			}
		}
		for _, f := range files {
			r, _ := filepath.Rel(root, f.Path)
			to := filepath.Join(dir, "model", fmt.Sprint(n), r)
			if err := copyFile(f.Path, to); err != nil {
				return err
			}
			f.Path = to
		}
	}
	for i := range m.Artifacts {
		if p := m.Artifacts[i].Path; !under(p) {
			to := place("artifacts", filepath.Base(p))
			if err := copyFile(p, to); err != nil {
				return fmt.Errorf("manifest: artifact %s: %w", m.Artifacts[i].Name, err)
			}
			m.Artifacts[i].Path = to
		}
	}
	return nil
}

// within reports whether p is in the directory root.
func within(root, p string) bool {
	r, err := filepath.Rel(root, p)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}

// copyFile copies the file at from to to, making to's directory.
func copyFile(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
