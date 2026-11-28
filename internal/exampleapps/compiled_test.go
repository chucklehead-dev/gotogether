package exampleapps

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// needsQuint is the list of what still needs quint among the examples
// (ROADMAP Q0): bindings that keep no compiled model, and tests of
// examples that keep one which stay on RequireQuint, each with what would
// release it and why. ci/compiled-examples.sh finds the models and the
// RequireModel tests; this file says the rest.
const needsQuint = "ci/needs-quint.txt"

// maxModel is the most a checked-in compiled model may take, as kept:
// its gzip container's bytes (validate.CompiledExt; the JSON inside is
// mostly quint's IR, about twenty times larger). A larger one is listed
// in needsQuint as size, for the maintainer to decide.
const maxModel = 1 << 20

// A quintNeed is a line of needsQuint.
type quintNeed struct {
	line         int
	path, test   string // a binding, or a package directory and its test
	release, why string
	seen         bool
}

func readNeeds(t *testing.T) []*quintNeed {
	f, err := os.Open(filepath.Join(root, needsQuint))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []*quintNeed
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		parts := strings.SplitN(l, " | ", 3)
		if len(parts) != 3 || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
			t.Errorf("%s:%d: want \"<binding> | <released by> | <why>\" or \"<package dir> <Test> | <released by> | <why>\": %q", needsQuint, n, l)
			continue
		}
		q := &quintNeed{line: n, release: strings.TrimSpace(parts[1]), why: strings.TrimSpace(parts[2])}
		fields := strings.Fields(parts[0])
		switch len(fields) {
		case 1:
			q.path = fields[0]
		case 2:
			q.path, q.test = fields[0], fields[1]
		default:
			t.Errorf("%s:%d: %q: a binding, or a package directory and one test", needsQuint, n, parts[0])
			continue
		}
		out = append(out, q)
	}
	return out
}

// isBinding is doctor's rule (cmd/gotogether, findBindings): a YAML file
// with a top-level spec and module key.
func isBinding(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var spec, module bool
	for _, l := range strings.Split(string(b), "\n") {
		spec = spec || strings.HasPrefix(l, "spec:")
		module = module || strings.HasPrefix(l, "module:")
	}
	return spec && module
}

// compiledFor is validate.CompiledFor, which this package cannot import
// (it reads the repository's files only): stock.binding.yaml's model is
// stock.model.json.gz, account.yaml's account.model.json.gz.
func compiledFor(binding string) string {
	dir, name := filepath.Split(binding)
	name = strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
	name = strings.TrimSuffix(name, ".binding")
	return filepath.Join(dir, name+".model.json.gz")
}

// readModel is the JSON document of a checked-in compiled model, its
// gzip container opened.
func readModel(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

// walkExamples calls f for every file under examples/ outside
// node_modules, gosim's translations, hidden directories and directories
// starting with "_", with its path relative to the repository. The go
// command ignores "_" directories as ./... does, and cmd/gotogether's
// scaffold and init tests make their temporary projects there
// (examples/_scaffolded*) while this test may be walking: one listed then
// failed moduleOf as "in no module".
func walkExamples(t *testing.T, f func(rel string)) {
	base := filepath.Join(root, "examples")
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); p != base && (n == "node_modules" || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		f(filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

var (
	testFunc    = regexp.MustCompile(`^func ((?:Test|Fuzz)\w*)\(`)
	otherFunc   = regexp.MustCompile(`^func `)
	needsQuintC = regexp.MustCompile(`qtest\.(RequireQuint|Model)\(`)
	modelC      = regexp.MustCompile(`qtest\.RequireModel\(`)
)

// testCalls are the package's test functions, each with whether its body
// calls RequireModel and whether it calls RequireQuint or Model (quint's
// own checks).
func testCalls(t *testing.T, dir string) map[string][2]bool {
	files, _ := filepath.Glob(filepath.Join(root, dir, "*_test.go"))
	out := map[string][2]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		fn := ""
		for _, l := range strings.Split(string(b), "\n") {
			if m := testFunc.FindStringSubmatch(l); m != nil {
				fn = m[1]
				out[fn] = [2]bool{}
				continue
			}
			if otherFunc.MatchString(l) {
				fn = ""
			}
			if fn == "" || strings.HasPrefix(strings.TrimSpace(l), "//") {
				continue
			}
			c := out[fn]
			c[0] = c[0] || modelC.MatchString(l)
			c[1] = c[1] || needsQuintC.MatchString(l)
			out[fn] = c
		}
	}
	return out
}

// Every binding of an example keeps its compiled model beside it, or is
// listed in ci/needs-quint.txt with what would release it; every model is
// of a binding there, within maxModel; and in each module that keeps a
// model, every test left needing quint (RequireQuint, Model) is listed
// with its reason, and none listed has moved to RequireModel. A model
// added, removed or grown, or a test moved, without the list changing,
// fails here (ci/compiled-examples.sh checks the models current and runs
// the RequireModel tests without quint). A spec's compiled model for
// connect is kept gzipped too (a plain .mbt.json is not found), within
// maxModel.
func TestCompiledModels(t *testing.T) {
	needs := readNeeds(t)
	byPath := map[string]*quintNeed{}
	byTest := map[string]*quintNeed{}
	for _, q := range needs {
		key := q.path
		m := byPath
		if q.test != "" {
			key, m = q.path+" "+q.test, byTest
		}
		if m[key] != nil {
			t.Errorf("%s:%d: %s is listed again (line %d)", needsQuint, q.line, key, m[key].line)
		}
		m[key] = q
	}
	var bindings, models []string
	walkExamples(t, func(rel string) {
		switch {
		case strings.HasSuffix(rel, ".model.json.gz"):
			models = append(models, rel)
		case strings.HasSuffix(rel, ".model.json"):
			t.Errorf("%s is a compiled model kept plain: the examples keep theirs gzipped beside the binding (validate.CompiledFor, .model.json.gz); remove it and compile it there (gotogether compile -binding B)", rel)
		case strings.HasSuffix(rel, ".mbt.json"):
			t.Errorf("%s is a compiled model for connect kept plain, which connect does not find: the examples keep theirs gzipped beside the spec (connect.CompiledFor, .mbt.json.gz); remove it and compile it there (gotogether compile -spec S [-main M])", rel)
		case strings.HasSuffix(rel, ".mbt.json.gz"):
			if fi, err := os.Stat(filepath.Join(root, rel)); err == nil && fi.Size() > maxModel {
				t.Errorf("%s is %.2f MB compressed, over the %d MB a checked-in compiled model may take: keep the spec's connect tests on quint (ci/needs-quint.txt)", rel, float64(fi.Size())/1e6, maxModel>>20)
			}
		case (strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml")) && isBinding(filepath.Join(root, rel)):
			bindings = append(bindings, rel)
		}
	})
	if len(bindings) < 20 {
		t.Fatalf("only %d bindings under examples/: is the walk wrong?", len(bindings))
	}
	modules := map[string]bool{} // modules keeping a model
	for _, b := range bindings {
		model := filepath.ToSlash(compiledFor(b))
		_, err := os.Stat(filepath.Join(root, model))
		has := err == nil
		q := byPath[b]
		if q != nil {
			q.seen = true
		}
		switch {
		case has && q != nil:
			t.Errorf("%s:%d: %s keeps its compiled model %s now: drop its line", needsQuint, q.line, b, model)
		case !has && q == nil:
			t.Errorf("%s has no compiled model at %s: compile it (gotogether compile -binding %s, where quint is) and move its tests to qtest.RequireModel, or list it in %s with what would release it", b, model, b, needsQuint)
		}
		if has {
			modules[moduleOf(t, filepath.Dir(b))] = true
		}
	}
	for _, q := range needs {
		if q.test == "" && !q.seen {
			t.Errorf("%s:%d: %s is no binding under examples/: drop its line", needsQuint, q.line, q.path)
		}
	}
	for _, m := range models {
		fi, err := os.Stat(filepath.Join(root, m))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Size() > maxModel {
			t.Errorf("%s is %.2f MB compressed, over the %d MB a checked-in compiled model may take: remove it and list its binding in %s as size", m, float64(fi.Size())/1e6, maxModel>>20, needsQuint)
		}
		var art struct {
			Binding struct{ Path string } `json:"binding"`
		}
		b, err := readModel(filepath.Join(root, m))
		if err == nil {
			err = json.Unmarshal(b, &art)
		}
		if err != nil || art.Binding.Path == "" {
			t.Errorf("%s names no binding (%v): is it gotogether compile's?", m, err)
			continue
		}
		bp := filepath.ToSlash(filepath.Join(filepath.Dir(m), art.Binding.Path))
		if !slices.Contains(bindings, bp) || filepath.ToSlash(compiledFor(bp)) != m {
			t.Errorf("%s is the compiled model of %s, which is not a binding beside it (validate.CompiledFor): recompile it beside its binding, or remove it", m, bp)
		}
	}
	// the tests of modules keeping a model
	pkgs := map[string]bool{}
	walkExamples(t, func(rel string) {
		if strings.HasSuffix(rel, "_test.go") && !strings.Contains(rel, "/testdata/") && modules[moduleOf(t, filepath.Dir(rel))] {
			pkgs[filepath.Dir(rel)] = true
		}
	})
	for _, p := range slices.Sorted(maps.Keys(pkgs)) {
		for fn, c := range testCalls(t, p) {
			q := byTest[p+" "+fn]
			if q != nil {
				q.seen = true
			}
			switch {
			case q != nil && c[0]:
				t.Errorf("%s:%d: %s's %s calls qtest.RequireModel: its runs are judged without quint, drop its line", needsQuint, q.line, p, fn)
			case q == nil && c[1] && !c[0]:
				t.Errorf("%s's %s needs quint (qtest.RequireQuint or qtest.Model) in a module that keeps a compiled model: move it to qtest.RequireModel if the model alone judges it (GOTOGETHER_COMPILED=1, quint off PATH), else list it in %s with what would release it", p, fn, needsQuint)
			case q != nil && !c[1]:
				t.Errorf("%s:%d: %s's %s calls neither RequireQuint nor Model now: drop its line", needsQuint, q.line, p, fn)
			}
		}
	}
	for _, q := range needs {
		if q.test != "" && !q.seen {
			t.Errorf("%s:%d: %s has no test %s in a module that keeps a compiled model: drop its line", needsQuint, q.line, q.path, q.test)
		}
	}
}

// moduleOf is the directory of the go.mod nearest above dir (relative to
// the repository).
func moduleOf(t *testing.T, dir string) string {
	for d := dir; d != "." && d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(root, d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("%s is in no module", dir)
	return ""
}
