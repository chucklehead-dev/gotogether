// Package qfront is the Go front end for Quint (Q2, docs/q2-front-end.md):
// Quint text into the documents quint writes, so that gotogether needs
// quint only as the oracle that checks it (internal/qoracle).
//
// P1 is built: Parse is `quint parse`'s parsing and source resolution
// (its phases 1 and 2), writing its modules and source map as
// `quint parse --out --source-map` writes them. Name resolution, the
// toposort, types, effects and flattening (P2 to P4) are not: the parse's
// output has no lookup table, its declarations are in the order written,
// and errors of those phases are not reported.
package qfront

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/qfront/ir"
	"github.com/chucklehead-dev/gotogether/internal/qfront/syntax"
	"github.com/chucklehead-dev/gotogether/internal/quintnames"
)

// Parsed is a program parsed: its modules (the main file's, then each
// imported file's in the order quint loads them), its errors and its
// source map.
type Parsed struct {
	Modules   []*ir.Module
	Errors    []syntax.Error
	SourceMap []syntax.SourceEntry
}

// ReadFile reads an imported file (os.ReadFile, or a test's files).
type ReadFile func(name string) ([]byte, error)

// Parse parses the file at path (made absolute, as quint names it) and
// the files its modules import.
func Parse(file string) (*Parsed, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	text, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	return ParseText(abs, string(text), os.ReadFile), nil
}

// ParseText is Parse of the main file's text, named file, its imports
// read by read. It never fails: what it cannot parse is in Errors.
//
// It is quint's parsePhase1fromText and parsePhase2sourceResolution: a
// worklist of modules, each import or instance `from` a file not yet
// loaded parsing that file (its path the importer's directory joined
// with the path given and ".qnt", its text's line ends made LF) with the
// same id generator. As quint's, it stops at the first file it cannot
// load (QNT013) or that differs from a loaded one only in case (QNT408),
// keeping then the main file's modules alone, and it reports no error of
// an imported file's parse (quint's phase 2 drops them).
func ParseText(file, text string, read ReadFile) *Parsed {
	gen := &ir.IDGen{}
	main := syntax.Parse(text, file, gen)
	out := &Parsed{Errors: main.Errors, SourceMap: main.SourceMap}
	mainNorm := filepath.Clean(file)
	type work struct {
		m     *ir.Module
		trail []string
	}
	var wl []work
	for _, m := range main.Modules {
		wl = append(wl, work{m, []string{mainNorm}})
	}
	files := [][]*ir.Module{main.Modules}
	visited := map[string]string{strings.ToLower(mainNorm): mainNorm}
	locOf := func(id ir.ID) ir.Loc {
		for _, e := range out.SourceMap {
			if e.ID == id {
				return e.Loc
			}
		}
		return ir.Loc{Source: file}
	}
	fail := func(code, msg string, id ir.ID) *Parsed {
		out.Errors = append(out.Errors, syntax.Error{Code: code, Msg: msg, Loc: locOf(id), ID: id})
		out.Modules = finish([][]*ir.Module{main.Modules})
		return out
	}
	for len(wl) > 0 {
		w := wl[0]
		wl = wl[1:]
		for _, d := range w.m.Decls {
			if (d.Kind != ir.KindImport && d.Kind != ir.KindInstance) || d.FromSource == nil {
				continue
			}
			stem := filepath.Dir(w.trail[len(w.trail)-1])
			rel := *d.FromSource + ".qnt"
			importee := filepath.Clean(filepath.Join(stem, rel))
			lower := strings.ToLower(importee)
			if seen, ok := visited[lower]; ok {
				if seen == importee {
					continue
				}
				return fail("QNT408", fmt.Sprintf("Importing two files that only differ in case: %s vs. %s. Choose one way.", seen, importee), d.ID)
			}
			b, err := read(importee)
			if err != nil {
				return fail("QNT013", fmt.Sprintf("import ... from '%s': could not load", *d.FromSource), d.ID)
			}
			src := strings.ReplaceAll(string(b), "\r\n", "\n")
			r := syntax.Parse(src, path.Join(filepath.ToSlash(stem), rel), gen)
			mods := slices.Clone(r.Modules)
			slices.Reverse(mods)
			for _, m := range mods {
				wl = append(wl, work{m, append(slices.Clone(w.trail), importee)})
			}
			files = append(files, mods)
			visited[lower] = importee
			out.SourceMap = append(out.SourceMap, r.SourceMap...)
		}
	}
	out.Modules = finish(files)
	return out
}

// finish is the modules of every file in order, with the depth quint's
// name collector writes on the declarations it collects: 0 on each
// top-level var, const, typedef and assume, unless it is named `_` or
// redefines a built-in (names/collector.ts, `def.depth ??= 0`). quint
// writes it in its parse's output, so P1 writes it too.
func finish(files [][]*ir.Module) []*ir.Module {
	var out []*ir.Module
	for _, ms := range files {
		for _, m := range ms {
			for _, d := range m.Decls {
				switch d.Kind {
				case ir.KindVar, ir.KindConst, ir.KindTypeDef, ir.KindAssume:
					if d.Name != "_" && !slices.Contains(quintnames.Builtins, d.Name) {
						zero := 0
						d.Depth = &zero
					}
				}
			}
			out = append(out, m)
		}
	}
	return out
}

// JSON is the parse as `quint parse --out` writes it, less its lookup
// table (name resolution's, P2).
func (p *Parsed) JSON() []byte {
	var b bytes.Buffer
	b.WriteString(`{"stage":"parsing","warnings":[],"modules":`)
	mods := p.Modules
	if mods == nil {
		mods = []*ir.Module{}
	}
	m, err := json.Marshal(mods)
	if err != nil {
		panic(err) // the IR's encoders fail on nothing
	}
	b.Write(m)
	b.WriteString(`,"errors":`)
	b.Write(ErrorsJSON(p.Errors))
	b.WriteString("}")
	return b.Bytes()
}

// ErrorsJSON is errors as quint writes them: an explanation with its
// code, and its location.
func ErrorsJSON(errs []syntax.Error) []byte {
	type loc struct {
		Source string `json:"source"`
		Start  ir.Pos `json:"start"`
		End    ir.Pos `json:"end"`
	}
	type msg struct {
		Explanation string `json:"explanation"`
		Locs        []loc  `json:"locs"`
	}
	out := []msg{}
	for _, e := range errs {
		out = append(out, msg{fmt.Sprintf("[%s] %s", e.Code, e.Msg), []loc{{e.Loc.Source, e.Loc.Start, e.Loc.End}}})
	}
	b, err := json.Marshal(out)
	if err != nil {
		panic(err)
	}
	return b
}

// SourceMapJSON is the source map as `quint parse --source-map` writes
// it: a file index, then each id's file, start and end.
func (p *Parsed) SourceMapJSON() []byte {
	index := map[string]int{}
	var files []string
	var b bytes.Buffer
	b.WriteString(`{"sourceIndex":{`)
	var m bytes.Buffer
	for i, e := range p.SourceMap {
		k, ok := index[e.Loc.Source]
		if !ok {
			k = len(files)
			index[e.Loc.Source] = k
			files = append(files, e.Loc.Source)
		}
		if i > 0 {
			m.WriteByte(',')
		}
		start, _ := json.Marshal(e.Loc.Start)
		end := []byte("{}")
		if !e.Loc.NoEnd {
			end, _ = json.Marshal(e.Loc.End)
		}
		fmt.Fprintf(&m, `"%d":[%d,%s,%s]`, e.ID, k, start, end)
	}
	for i, f := range files {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"%d":%s`, i, strconv.Quote(f))
	}
	b.WriteString(`},"map":{`)
	b.Write(m.Bytes())
	b.WriteString("}}")
	return b.Bytes()
}
