package qoracle

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// The corpus (docs/q2-front-end.md §4.1): every program Q2's front end is
// compared with quint on, pinned by a manifest (testdata/corpus.json).
// Each entry says where its program comes from, its text's sha256, the
// constructs it uses, what compile is given, and quint's verdict at the
// pinned version. Sources:
//   - repo: the repository's .qnt files;
//   - quint: quint's examples/ and quint/testFixture at QUINT_VERSION's
//     tag (ci/fetch-quint-src.sh), never vendored;
//   - quint-test: the inline programs of quint's unit tests
//     (ExtractPrograms), each wrapped as the tests' builders wrap it;
//   - eval and invariant: Quint gotogether generates, from each binding of
//     the repository: binding.EvalModule's text compiled as validate
//     compiles it, and the model compiled with --invariant as connect
//     compiles it.

// Sources of entries.
const (
	SourceRepo      = "repo"
	SourceQuint     = "quint"
	SourceQuintTest = "quint-test"
	SourceEval      = "eval"
	SourceInvariant = "invariant"
)

// Expectations.
const (
	// ExpectAccept: quint accepts it; so must the front end.
	ExpectAccept = "accept"
	// ExpectReject: quint refuses it; so must the front end, with the
	// same first error.
	ExpectReject = "reject"
	// ExpectNeedsQuint: it uses apalache::, which the Go front end refuses
	// at first as "needs quint" (decision 4, revised).
	ExpectNeedsQuint = "needs-quint"
)

// A Verdict is quint's answer: the codes of its errors, in order, for the
// typecheck and, where the entry compiles, the compile ([] where it
// accepts).
type Verdict struct {
	Typecheck []string `json:"typecheck"`
	Compile   []string `json:"compile,omitempty"`
}

// An Entry is one program of the corpus.
type Entry struct {
	// ID is source:path, with #n for a test file's n-th program.
	ID     string `json:"id"`
	Source string `json:"source"`
	// Path is the file, relative to its source's root (the repository's,
	// quint's checkout); for eval and invariant entries, the binding.
	Path  string `json:"path"`
	Index int    `json:"index,omitempty"`
	// SHA256 is the program's text's.
	SHA256     string   `json:"sha256"`
	Lines      int      `json:"lines"`
	Constructs []string `json:"constructs,omitempty"`
	// Main, Init, Step and Invariants are compile's: an entry with no Main
	// is parsed and typechecked only.
	Main       string   `json:"main,omitempty"`
	Init       string   `json:"init,omitempty"`
	Step       string   `json:"step,omitempty"`
	Invariants []string `json:"invariants,omitempty"`
	Quint      Verdict  `json:"quint"`
	Expect     string   `json:"expect"`
}

// A Manifest is the pinned corpus.
type Manifest struct {
	// Quint is the version whose files and verdicts it holds, and Commit
	// its tag's commit (ci/quint-src.commit).
	Quint   string  `json:"quint"`
	Commit  string  `json:"commit"`
	Entries []Entry `json:"entries"`
}

// manifestJSON is the pinned manifest (read-only).
//
//go:embed testdata/corpus.json
var manifestJSON []byte

// LoadManifest is the pinned manifest.
func LoadManifest() (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return nil, fmt.Errorf("qoracle: testdata/corpus.json: %w", err)
	}
	return &m, nil
}

// Encode is the manifest as testdata/corpus.json holds it: one entry a
// line, so that a change shows as the entries it changes.
func (m *Manifest) Encode() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "{\"quint\": %q, \"commit\": %q, \"entries\": [\n", m.Quint, m.Commit)
	for i, e := range m.Entries {
		line, err := json.Marshal(e)
		if err != nil {
			panic(err)
		}
		b.Write(line)
		if i < len(m.Entries)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("]}\n")
	return b.Bytes()
}

func sha(text string) string {
	s := sha256.Sum256([]byte(text))
	return hex.EncodeToString(s[:])
}

// constructRules name the constructs an entry uses, read from its text
// with comments taken out (stripComments): what Q2's phases must parse,
// and what decision 4 accepts (destructuring, temporal, assume) or
// refuses (apalache::). Read-only.
var constructRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{"import", regexp.MustCompile(`(?m)^\s*import\s`)},
	{"import-from", regexp.MustCompile(`\bfrom\s*"`)},
	{"instance", regexp.MustCompile(`\bimport\s+[A-Za-z_][\w:]*\s*\(`)},
	{"export", regexp.MustCompile(`(?m)^\s*export\s`)},
	{"const", regexp.MustCompile(`(?m)^\s*const\s`)},
	{"var", regexp.MustCompile(`(?m)^\s*var\s`)},
	{"type-alias", regexp.MustCompile(`\btype\s+[A-Z]\w*\s*=`)},
	{"poly-alias", regexp.MustCompile(`\btype\s+[A-Z]\w*\s*\[`)},
	{"sum-type", regexp.MustCompile(`\btype\s+[A-Z]\w*(\[[^\]]*\])?\s*=\s*\|?\s*[A-Z]\w*\s*(\(|\|)`)},
	{"match", regexp.MustCompile(`\bmatch\b`)},
	{"spread", regexp.MustCompile(`\.\.\.`)},
	{"record", regexp.MustCompile(`\{\s*[a-z_]\w*\s*:`)},
	{"lambda", regexp.MustCompile(`=>`)},
	{"nondet", regexp.MustCompile(`\bnondet\b`)},
	{"action", regexp.MustCompile(`\baction\b`)},
	{"run", regexp.MustCompile(`(?m)^\s*run\s`)},
	{"temporal", regexp.MustCompile(`(?m)^\s*temporal\s`)},
	{"temporal-ops", regexp.MustCompile(`\b(always|eventually|weakFair|strongFair|leadsTo)\b`)},
	{"assume", regexp.MustCompile(`(?m)^\s*assume\s`)},
	{"destructuring", regexp.MustCompile(`\bval\s*[({]|\(\(\s*[a-z_]\w*\s*,`)},
	{"apalache", regexp.MustCompile(`\bapalache::`)},
	{"hex", regexp.MustCompile(`\b0x[0-9a-fA-F]`)},
	{"doc-comment", regexp.MustCompile(`(?m)^\s*///`)},
	{"pure", regexp.MustCompile(`\bpure\s+(val|def)\b`)},
}

// Constructs are the constructs text uses, by constructRules.
func Constructs(text string) []string {
	plain := stripComments(text)
	var out []string
	for _, r := range constructRules {
		src := plain
		if r.name == "doc-comment" {
			src = text
		}
		if r.re.MatchString(src) {
			out = append(out, r.name)
		}
	}
	return out
}

func expectOf(e Entry) string {
	if slices.Contains(e.Constructs, "apalache") {
		return ExpectNeedsQuint
	}
	if len(e.Quint.Typecheck) > 0 || len(e.Quint.Compile) > 0 {
		return ExpectReject
	}
	return ExpectAccept
}

// Roots are where the corpus's sources are.
type Roots struct {
	// Repo is the repository's root, Quint quint's checkout at the tag
	// ("" when not fetched: its entries are skipped).
	Repo, Quint string
}

// skipDir is a directory the repository's walk leaves out: hidden ones
// (.git, a worktree's .claude, gosim's .gosim), node_modules.
func skipDir(name string) bool {
	return name != "." && (strings.HasPrefix(name, ".") || name == "node_modules")
}

// qntFiles are the .qnt files under dir, relative, sorted.
func qntFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".qnt") {
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	slices.Sort(out)
	return out, err
}

// repoQntFiles are the repository's .qnt files, relative, sorted: those
// git tracks or would track (.gitignore honoured), never the modules tests
// write into the tree (validate's literate counter.qnt, binding's
// __gtg_eval_ modules), which a walk found once a test had run and pinned
// files a clean checkout lacks. A tracked file deleted in the work tree is
// left out, as the walk would.
func repoQntFiles(dir string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "*.qnt")
	cmd.Dir = dir
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing the repository's .qnt files with git ls-files in %s: %w", dir, err)
	}
	var out []string
	for _, rel := range strings.Split(string(raw), "\x00") {
		if rel == "" || slices.ContainsFunc(strings.Split(rel, "/"), skipDir) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			continue
		}
		out = append(out, rel)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// quintDirs are quint's checkout's corpus directories (read-only).
var quintDirs = []string{"examples", "quint/testFixture"}

// Files are the corpus's file entries (repo, quint, quint-test) found
// under roots, each with its text and constructs; their verdicts and
// compile settings are not known yet (Survey adds them).
func Files(roots Roots) ([]Entry, map[string]string, error) {
	var out []Entry
	texts := map[string]string{}
	addFile := func(source, root, rel string) error {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return err
		}
		id := source + ":" + rel
		texts[id] = string(b)
		out = append(out, Entry{ID: id, Source: source, Path: rel, SHA256: sha(string(b)),
			Lines: strings.Count(string(b), "\n") + 1, Constructs: Constructs(string(b))})
		return nil
	}
	files, err := repoQntFiles(roots.Repo)
	if err != nil {
		return nil, nil, err
	}
	for _, f := range files {
		if err := addFile(SourceRepo, roots.Repo, f); err != nil {
			return nil, nil, err
		}
	}
	if roots.Quint == "" {
		return out, texts, nil
	}
	for _, d := range quintDirs {
		files, err := qntFiles(filepath.Join(roots.Quint, d))
		if err != nil {
			return nil, nil, err
		}
		for _, f := range files {
			if err := addFile(SourceQuint, roots.Quint, d+"/"+f); err != nil {
				return nil, nil, err
			}
		}
	}
	var tests []string
	err = filepath.WalkDir(filepath.Join(roots.Quint, "quint/test"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".test.ts") {
			tests = append(tests, p)
		}
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	slices.Sort(tests)
	for _, p := range tests {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		rel, _ := filepath.Rel(roots.Quint, p)
		rel = filepath.ToSlash(rel)
		for i, prog := range ExtractPrograms(string(b)) {
			id := fmt.Sprintf("%s:%s#%d", SourceQuintTest, rel, i)
			texts[id] = prog
			out = append(out, Entry{ID: id, Source: SourceQuintTest, Path: rel, Index: i, SHA256: sha(prog),
				Lines: strings.Count(prog, "\n") + 1, Constructs: Constructs(prog)})
		}
	}
	return out, texts, nil
}

// Program is e ready to run: its file under roots, or its text (a test's
// program, a generated module) written at a place of tmp's.
func (e Entry) Program(roots Roots, text, tmp string) (Program, error) {
	p := Program{ID: e.ID, Main: e.Main, Init: e.Init, Step: e.Step, Invariants: e.Invariants}
	switch e.Source {
	case SourceRepo, SourceInvariant:
		path := e.Path
		if e.Source == SourceInvariant {
			path = invariantSpec(e)
		}
		p.File = filepath.Join(roots.Repo, path)
	case SourceQuint:
		if roots.Quint == "" {
			return p, fmt.Errorf("%s: quint's sources are not fetched (ci/fetch-quint-src.sh, QUINT_SRC)", e.ID)
		}
		p.File = filepath.Join(roots.Quint, e.Path)
	case SourceQuintTest:
		dir, err := os.MkdirTemp(tmp, "test-")
		if err != nil {
			return p, err
		}
		p.File, p.Text = filepath.Join(dir, "wrapper.qnt"), text
	case SourceEval:
		// at the place its import was written for (Generated)
		p.File, p.Text = evalPath(roots, e), text
	default:
		return p, fmt.Errorf("%s: no source %q", e.ID, e.Source)
	}
	if text != "" && e.Source != SourceQuintTest && e.Source != SourceEval {
		if sha(text) != e.SHA256 {
			return p, fmt.Errorf("%s: its text is not the pinned one (sha256 %s, pinned %s): GOTOGETHER_UPDATE_CORPUS=1 rewrites the manifest", e.ID, sha(text), e.SHA256)
		}
	}
	return p, nil
}

// invariantSpec is an invariant entry's model, which its ID names after
// the binding: invariant:<binding>@<spec>.
func invariantSpec(e Entry) string {
	if i := strings.LastIndex(e.ID, "@"); i >= 0 {
		return e.ID[i+1:]
	}
	return e.Path
}

// evalPath is where an eval entry's module is written: beside its
// binding, so that its import of the model is the binding's relative
// path (the module never lands there: a stage copies it).
func evalPath(roots Roots, e Entry) string {
	return filepath.Join(roots.Repo, filepath.Dir(e.Path), "__gtg_eval_"+strings.TrimSuffix(filepath.Base(e.Path), filepath.Ext(e.Path))+".qnt")
}

// lineCount is text's lines.
func lineCount(text string) int { return strings.Count(text, "\n") + 1 }
