package qoracle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/qoracle/canon"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
)

// Output is what a front end made of a program: quint's three JSON
// documents (nil where it did not run), paths relative to Root.
type Output struct {
	// Root is the directory the documents' paths are relative to.
	Root string
	// Parse and SourceMap are `quint parse --out --source-map`'s.
	Parse, SourceMap []byte
	// Typecheck is `quint typecheck --out`'s.
	Typecheck []byte
	// Compile is `quint compile --target json`'s IR, or, where it failed,
	// its --out document (the modules before flattening, with errors).
	Compile []byte
}

// A FrontEnd parses, typechecks and compiles a program, writing quint's
// JSON. Its first is quint itself (Quint); Q2's Go front end is the next.
type FrontEnd interface {
	// Name names the front end in reports.
	Name() string
	// Run is the front end's outputs for p, in a directory of dir's.
	Run(ctx context.Context, p Program, dir string) (*Output, error)
}

// Quint is quint as a front end: the oracle, and the stub "front end"
// the harness compares quint with before Q2 exists.
type Quint struct {
	// Bin is the quint binary ("" is "quint").
	Bin string
}

// Name is "quint".
func (Quint) Name() string { return "quint" }

// ErrTool is a front end that wrote no output: a tool failure, not a
// verdict (a crashed evaluator, a missing binary).
var ErrTool = errors.New("the front end wrote no output")

// Run runs quint's parse, typecheck and (with a Main) compile on a staged
// copy of p in dir.
func (q Quint) Run(ctx context.Context, p Program, dir string) (*Output, error) {
	file, err := stage(p, dir)
	if err != nil {
		return nil, err
	}
	return q.run(ctx, p, dir, file)
}

func (q Quint) run(ctx context.Context, p Program, root, file string) (*Output, error) {
	out := &Output{Root: root}
	work := filepath.Join(root, ".gtg-out")
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil, err
	}
	at := func(n string) string { return filepath.Join(work, n) }
	run := func(stdout string, args ...string) (string, error) {
		cmd := quintcmd.Command(ctx, q.Bin, filepath.Dir(file), args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		cmd.Stdout = io.Discard
		if stdout != "" {
			// a file, not a pipe: node exits before a pipe drains (as
			// validate/eval.go writes compile's IR)
			f, err := os.Create(stdout)
			if err != nil {
				return "", err
			}
			defer f.Close()
			cmd.Stdout = f
		}
		err := cmd.Run()
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			return stderr.String(), err
		}
		return stderr.String(), nil
	}
	read := func(path, what, stderr string) ([]byte, error) {
		b, err := os.ReadFile(path)
		if err != nil || len(bytes.TrimSpace(b)) == 0 {
			return nil, fmt.Errorf("%w: quint %s of %s: %s", ErrTool, what, p.ID, strings.TrimSpace(stderr))
		}
		return b, nil
	}
	stderr, err := run("", "parse", "--out", at("parse.json"), "--source-map", at("map.json"), file)
	if err != nil {
		return nil, err
	}
	if out.Parse, err = read(at("parse.json"), "parse", stderr); err != nil {
		return nil, err
	}
	// a failed parse writes no source map
	out.SourceMap, _ = os.ReadFile(at("map.json"))
	if stderr, err = run("", "typecheck", "--out", at("typecheck.json"), file); err != nil {
		return nil, err
	}
	if out.Typecheck, err = read(at("typecheck.json"), "typecheck", stderr); err != nil {
		return nil, err
	}
	if p.Main == "" {
		return out, nil
	}
	args := []string{"compile", "--target", "json", "--main", p.Main, "--init", or(p.Init, "init"), "--step", or(p.Step, "step")}
	if len(p.Invariants) > 0 {
		args = append(args, "--invariant", strings.Join(p.Invariants, ","))
	}
	// the IR is stdout's; --out writes the modules before flattening, with
	// the errors, which a failed compile writes nowhere else
	args = append(args, "--out", at("compile-errors.json"), file)
	if stderr, err = run(at("compile.json"), args...); err != nil {
		return nil, err
	}
	if b, _ := os.ReadFile(at("compile.json")); len(bytes.TrimSpace(b)) > 0 {
		out.Compile = b
	} else if out.Compile, err = read(at("compile-errors.json"), "compile", stderr); err != nil {
		return nil, err
	}
	return out, nil
}

func or(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// shiftModule consumes ids before every module of the program.
const shiftModule = "module __gtg_shift { pure val __gtg_s1 = 1 pure val __gtg_s2 = Set(1, 2) } "

// Shifted is quint run on the program with a module consuming ids written
// first, on the main file's first line, and that module stripped from
// its outputs, its columns on that line and its characters' offsets
// taken back. Every id differs from Quint's and nothing else should: the
// harness comparing Quint with Shifted checks the canonical form over the
// whole corpus, as Q2's front end, numbering its nodes otherwise, will
// need it to hold.
type Shifted struct {
	Quint
}

// Name is "quint-shifted".
func (Shifted) Name() string { return "quint-shifted" }

// Run runs quint on p shifted.
func (s Shifted) Run(ctx context.Context, p Program, dir string) (*Output, error) {
	file, err := stage(p, dir)
	if err != nil {
		return nil, err
	}
	text, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(file, append([]byte(shiftModule), text...), 0o644); err != nil {
		return nil, err
	}
	out, err := s.run(ctx, p, dir, file)
	if err != nil {
		return nil, err
	}
	rel, _ := filepath.Rel(dir, file)
	return unshift(out, rel, len(shiftModule))
}

// unshift takes the shift module out of out: the module, the entries of
// its ids, and its characters from the main file's positions.
func unshift(out *Output, file string, n int) (*Output, error) {
	_, ids, err := canon.StripModule(out.Typecheck, "__gtg_shift", nil)
	if err != nil {
		return nil, err
	}
	pids, err := moduleIDs(out.Parse, "__gtg_shift")
	if err != nil {
		return nil, err
	}
	for k := range pids {
		ids[k] = true
	}
	fix := func(doc []byte, src func(string) bool) ([]byte, error) {
		if doc == nil {
			return nil, nil
		}
		stripped, _, err := canon.StripModule(doc, "__gtg_shift", ids)
		if err != nil {
			return nil, err
		}
		return shiftErrors(stripped, src, n)
	}
	isMain := func(s string) bool { return strings.HasSuffix(filepath.ToSlash(s), "/"+filepath.ToSlash(file)) }
	if out.Parse, err = fix(out.Parse, isMain); err != nil {
		return nil, err
	}
	if out.Typecheck, err = fix(out.Typecheck, isMain); err != nil {
		return nil, err
	}
	if out.Compile, err = fix(out.Compile, isMain); err != nil {
		return nil, err
	}
	if len(out.SourceMap) > 0 {
		sm, err := canon.StripSpans(out.SourceMap, ids)
		if err != nil {
			return nil, err
		}
		if out.SourceMap, err = shiftSpans(sm, isMain, n); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func moduleIDs(doc []byte, name string) (map[string]bool, error) {
	_, ids, err := canon.StripModule(doc, name, nil)
	return ids, err
}

// back is a position {line, col, index} less the shift: n columns on the
// first line, n characters everywhere.
func back(v any, n int) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	num := func(k string) (int, bool) {
		x, ok := m[k].(json.Number)
		if !ok {
			return 0, false
		}
		i, err := x.Int64()
		return int(i), err == nil
	}
	if line, ok := num("line"); ok && line == 0 {
		if c, ok := num("col"); ok {
			m["col"] = json.Number(fmt.Sprint(c - n))
		}
	}
	if i, ok := num("index"); ok {
		m["index"] = json.Number(fmt.Sprint(i - n))
	}
}

// backSpan takes the shift back from a span. quint's end column of a node
// that starts on the first line is shifted wherever it ends (a module's
// closing brace lines below), so it is taken back when the start is.
func backSpan(start, end any, n int) {
	if lineOf(start) == 0 && lineOf(end) > 0 {
		if m, ok := end.(map[string]any); ok {
			if c, ok := m["col"].(json.Number); ok {
				if i, err := c.Int64(); err == nil {
					m["col"] = json.Number(fmt.Sprint(int(i) - n))
				}
			}
		}
	}
	back(start, n)
	back(end, n)
}

func lineOf(v any) int {
	m, _ := v.(map[string]any)
	if l, ok := m["line"].(json.Number); ok {
		if i, err := l.Int64(); err == nil {
			return int(i)
		}
	}
	return -1
}

func decodeJSON(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	err := d.Decode(&v)
	return v, err
}

func shiftErrors(doc []byte, isMain func(string) bool, n int) ([]byte, error) {
	v, err := decodeJSON(doc)
	if err != nil {
		return nil, err
	}
	top, _ := v.(map[string]any)
	es, _ := top["errors"].([]any)
	for _, e := range es {
		em, _ := e.(map[string]any)
		locs, _ := em["locs"].([]any)
		for _, l := range locs {
			lm, _ := l.(map[string]any)
			if s, _ := lm["source"].(string); lm != nil && isMain(s) {
				backSpan(lm["start"], lm["end"], n)
			}
		}
	}
	return json.Marshal(top)
}

func shiftSpans(sm []byte, isMain func(string) bool, n int) ([]byte, error) {
	v, err := decodeJSON(sm)
	if err != nil {
		return nil, err
	}
	top, _ := v.(map[string]any)
	idx, _ := top["sourceIndex"].(map[string]any)
	mains := map[string]bool{}
	for k, s := range idx {
		if str, _ := s.(string); isMain(str) {
			mains[k] = true
		}
	}
	m, _ := top["map"].(map[string]any)
	for _, e := range m {
		l, _ := e.([]any)
		if len(l) == 3 && mains[fmt.Sprint(l[0])] {
			// quint's end column of a node that starts on the first line is
			// shifted wherever it ends (a module's closing brace two lines
			// down): taken back when the start is
			backSpan(l[1], l[2], n)
		}
	}
	return json.Marshal(top)
}
