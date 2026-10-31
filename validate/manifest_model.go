package validate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/atomicdir"
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
	if md.Compiled != nil {
		return md.verifyCompiled()
	}
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
		return []error{fmt.Errorf("the spec %s%s: %w: replay judges the model the manifest hashed; put it back where the binding finds it (a manifest's directory moves whole, with the model beside the binding as it was)", spec, how, err)}
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

// Bundle copies the manifest's complete, hash-verified file graph into a fresh
// directory, preserving relative references between model files. It refuses
// absolute references inside bindings and Quint sources, incomplete metadata,
// changed artifacts and existing destinations. Source symlinks to regular files
// are materialized as regular copied bytes, never copied as symlinks.
//
// Publication is atomic and does not replace existing evidence. On error the
// receiver and destination are unchanged. On success its paths name the copies;
// Write can then place a native manifest beside them. This checks export
// integrity, not the recorded run's verdict, and does not rerun the model.
func (m *Manifest) Bundle(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("manifest bundle: %s already exists: choose a fresh directory", dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := m.verifyBundle(""); err != nil {
		return err
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+".stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	c := m.copy()
	if err := c.copyBundle(stage); err != nil {
		return err
	}
	if err := c.verifyBundle(stage); err != nil {
		return err
	}
	if err := atomicdir.Publish(stage, dir); err != nil {
		return err
	}
	c.paths(func(p string) string {
		rel, _ := filepath.Rel(stage, p)
		return filepath.Join(dir, rel)
	})
	*m = *c
	return nil
}

type bundleFile struct {
	path *string
	hash string
}

// bundleFiles includes every referenced file, including specs and imports that
// Verify normally checks through their binding's resolution instead.
func (m *Manifest) bundleFiles() []bundleFile {
	var out []bundleFile
	for _, md := range m.models() {
		for _, f := range []*File{md.Binding, md.Spec, md.Convert, md.Compiled} {
			if f != nil {
				out = append(out, bundleFile{&f.Path, f.SHA256})
			}
		}
		for i := range md.Imports {
			f := &md.Imports[i]
			out = append(out, bundleFile{&f.Path, f.SHA256})
		}
	}
	for i := range m.Inputs {
		f := &m.Inputs[i].File
		out = append(out, bundleFile{&f.Path, f.SHA256})
	}
	for i := range m.Artifacts {
		f := &m.Artifacts[i]
		out = append(out, bundleFile{&f.Path, f.SHA256})
	}
	return out
}

// verifyBundle additionally proves that the explicit files describe the entire
// executable model graph. specFiles deliberately leaves missing imports for
// Quint to diagnose; a portable export must refuse that incomplete graph now.
func (m *Manifest) verifyBundle(root string) error {
	if m.Format != ManifestFormat {
		return fmt.Errorf("manifest bundle: format %q: want %s", m.Format, ManifestFormat)
	}
	for _, f := range m.bundleFiles() {
		p := *f.path
		if !filepath.IsAbs(p) || f.hash == "" {
			return fmt.Errorf("manifest bundle: %q needs an absolute resolved path and recorded hash", p)
		}
		if root != "" && !within(root, p) {
			return fmt.Errorf("manifest bundle: file %s escapes the staged directory", p)
		}
		info, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("manifest bundle: %w", err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("manifest bundle: %s is not a regular file", p)
		}
		hash, err := hashFile(p)
		if err != nil {
			return err
		}
		if hash != f.hash {
			return fmt.Errorf("manifest bundle: %s changed since the run was judged: restore the original evidence", p)
		}
	}
	if warnings, err := m.Verify(); err != nil {
		return fmt.Errorf("manifest bundle: %w", err)
	} else if len(warnings) > 0 {
		return fmt.Errorf("manifest bundle: %s", strings.Join(warnings, "; "))
	}
	for _, md := range m.models() {
		if md.Compiled != nil {
			if err := md.verifyCompiledBundle(root); err != nil {
				return err
			}
			continue
		}
		if md.Binding != nil {
			b, err := binding.Load(md.Binding.Path)
			if err != nil {
				return err
			}
			if len(importPaths(b.Defs)) > 0 {
				return fmt.Errorf("manifest bundle: binding %s imports files from defs, whose dependencies were not recorded: move file imports into the model and validate anew", md.Binding.Path)
			}
			if absoluteReference(b.Spec) {
				return fmt.Errorf("manifest bundle: binding %s has an absolute spec reference %q: validate anew with a relative spec before exporting", md.Binding.Path, b.Spec)
			}
			if md.Spec == nil || filepath.Clean(b.SpecPath()) != filepath.Clean(md.Spec.Path) {
				return fmt.Errorf("manifest bundle: binding %s resolves a spec absent from its recorded model graph", md.Binding.Path)
			}
		}
		if md.Spec == nil {
			if len(md.Imports) > 0 {
				return fmt.Errorf("manifest bundle: imports have no recorded main spec")
			}
			continue
		}
		graph := map[string]bool{filepath.Clean(md.Spec.Path): true}
		sources := []File{*md.Spec}
		sources = append(sources, md.Imports...)
		for _, f := range md.Imports {
			graph[filepath.Clean(f.Path)] = true
		}
		for _, f := range sources {
			src, err := os.ReadFile(f.Path)
			if err != nil {
				return err
			}
			for _, ref := range importPaths(string(src)) {
				if absoluteReference(ref) {
					return fmt.Errorf("manifest bundle: spec %s has an absolute import reference %q: validate anew with relative imports before exporting", f.Path, ref)
				}
				target := filepath.Join(filepath.Dir(f.Path), filepath.FromSlash(ref))
				if filepath.Ext(target) != ".qnt" {
					target += ".qnt"
				}
				if !graph[filepath.Clean(target)] || root != "" && !within(root, target) {
					return fmt.Errorf("manifest bundle: import %q in %s is absent from the recorded model graph: restore the complete model and validate anew", ref, f.Path)
				}
			}
		}
	}
	for _, in := range m.Inputs {
		switch in.Format {
		case "steps", "otlp", "requests":
			continue
		case "itf":
		default:
			return fmt.Errorf("manifest bundle: input %s has unresolved or unsupported format %q: validate anew to record its actual input format before exporting", in.Path, in.Format)
		}
		if in.Model == nil || in.Model.Spec == nil {
			return fmt.Errorf("manifest bundle: ITF input %s has no recorded model graph: validate anew before exporting", in.Path)
		}
		if m.Model.Binding != nil || in.Model.Binding != nil {
			continue
		} // binding defs replace metadata defs
		meta, err := bundleITFMeta(in.Path)
		if err != nil {
			return err
		}
		if defs, ok := binding.ReadITFMeta(meta); ok && len(importPaths(defs.Defs)) > 0 {
			return fmt.Errorf("manifest bundle: ITF input %s imports files from metadata defs, whose dependencies were not recorded: move file imports into the model and validate anew", in.Path)
		}
	}

	return nil
}

// bundleITFMeta reads only metadata, skipping state values without building an
// ITF trace. Export does not reinterpret model values (notably big integers).
func bundleITFMeta(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("manifest bundle: ITF input %s must be a JSON object", path)
	}
	var meta map[string]any
	seenMeta := false
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if key == "#meta" {
			if seenMeta {
				return nil, fmt.Errorf("manifest bundle: ITF input %s contains duplicate #meta fields: validate a capture with unambiguous metadata before exporting", path)
			}
			seenMeta = true
			if err := dec.Decode(&meta); err != nil {
				return nil, err
			}
			continue
		}
		if err := skipBundleJSON(dec); err != nil {
			return nil, err
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return meta, nil
}

func skipBundleJSON(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); ok && (delim == '{' || delim == '[') {
		for dec.More() {
			if err := skipBundleJSON(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
	}
	return err
}

func absoluteReference(p string) bool {
	return filepath.IsAbs(p) || filepath.VolumeName(p) != "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\\`) || len(p) > 1 && p[1] == ':'
}

// copyBundle works on a private manifest and destination. Each model keeps its
// lexical file layout, including relative parent references within the graph.
func (m *Manifest) copyBundle(dir string) error {
	used := map[string]bool{}
	place := func(sub, base string) string {
		p := filepath.Join(dir, sub, base)
		for n := 2; used[p]; n++ {
			p = filepath.Join(dir, sub, fmt.Sprintf("%d-%s", n, base))
		}
		used[p] = true
		return p
	}
	for i := range m.Inputs {
		f := &m.Inputs[i].File
		to := place("inputs", filepath.Base(f.Path))
		if err := copyFile(f.Path, to); err != nil {
			return err
		}
		f.Path = to
	}
	for n, md := range m.models() {
		var files []*File
		for _, f := range []*File{md.Binding, md.Spec, md.Convert, md.Compiled} {
			if f != nil {
				files = append(files, f)
			}
		}
		for i := range md.Imports {
			files = append(files, &md.Imports[i])
		}
		if len(files) == 0 {
			continue
		}
		root := filepath.Dir(files[0].Path)
		var places []string
		for _, f := range files[1:] {
			places = append(places, f.Path)
		}
		if md.Compiled != nil {
			_, paths, err := md.compiledSources()
			if err != nil {
				return err
			}
			for _, f := range paths {
				places = append(places, f.Path)
			}
		}
		for _, p := range places {
			for !within(root, p) {
				next := filepath.Dir(root)
				if next == root {
					return fmt.Errorf("manifest bundle: model files do not share a filesystem root")
				}
				root = next
			}
		}
		for _, f := range files {
			rel, err := filepath.Rel(root, f.Path)
			if err != nil {
				return err
			}
			to := filepath.Join(dir, "model", fmt.Sprint(n), rel)
			if err := copyFile(f.Path, to); err != nil {
				return err
			}
			f.Path = to
		}
	}
	for i := range m.Artifacts {
		f := &m.Artifacts[i]
		to := place("artifacts", filepath.Base(f.Path))
		if err := copyFile(f.Path, to); err != nil {
			return fmt.Errorf("manifest bundle: artifact %s: %w", f.Name, err)
		}
		f.Path = to
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
