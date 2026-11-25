package qoracle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/qoracle/canon"
)

// Bindings are the repository's binding files (a YAML document whose
// top level names a spec), relative to its root, sorted.
func Bindings(repo string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != repo && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".yml") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.HasPrefix(b, []byte("spec:")) || bytes.Contains(b, []byte("\nspec:")) {
			rel, _ := filepath.Rel(repo, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	slices.Sort(out)
	return out, err
}

// Generated is the Quint gotogether generates from the repository's
// bindings: each binding's evaluation module (binding.EvalModule, which
// validate compiles with its own name as main and its init as init and
// step) and, where it names invariants, its model compiled with them
// (connect's --invariant). Loading a binding's model runs quint (the
// typecheck binding.LoadModel makes). A binding whose module cannot be
// written (EvalModule's error) or whose model quint refuses is skipped,
// with the reason in skipped.
func Generated(ctx context.Context, roots Roots) (entries []Entry, texts map[string]string, skipped []string, err error) {
	paths, err := Bindings(roots.Repo)
	if err != nil {
		return nil, nil, nil, err
	}
	texts = map[string]string{}
	for _, rel := range paths {
		b, err := binding.Load(filepath.Join(roots.Repo, rel))
		if err != nil {
			skipped = append(skipped, rel+": "+err.Error())
			continue
		}
		m, err := binding.LoadModel(ctx, "", b.SpecPath(), b.Module)
		if err != nil {
			skipped = append(skipped, rel+": "+firstLine(err.Error()))
			continue
		}
		spec, err := filepath.Rel(roots.Repo, b.SpecPath())
		if err != nil {
			return nil, nil, nil, err
		}
		spec = filepath.ToSlash(spec)
		e := Entry{ID: SourceEval + ":" + rel, Source: SourceEval, Path: rel}
		em, err := b.EvalModule(m, evalPath(roots, e))
		if err != nil {
			skipped = append(skipped, rel+": the evaluation module: "+firstLine(err.Error()))
		} else {
			e.SHA256, e.Lines, e.Constructs = sha(em.Text), lineCount(em.Text), Constructs(em.Text)
			e.Main, e.Init, e.Step = em.Name, em.Init, em.Init
			texts[e.ID] = em.Text
			entries = append(entries, e)
		}
		if len(b.Invariants) > 0 {
			text, err := os.ReadFile(b.SpecPath())
			if err != nil {
				return nil, nil, nil, err
			}
			entries = append(entries, Entry{ID: SourceInvariant + ":" + rel + "@" + spec, Source: SourceInvariant, Path: rel,
				SHA256: sha(string(text)), Lines: lineCount(string(text)), Constructs: Constructs(string(text)),
				Main: b.Module, Invariants: slices.Clone(b.Invariants)})
		}
	}
	return entries, texts, skipped, nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// Survey runs quint on each entry and records its verdict and, for a
// file with no compile settings yet, the module compile is given: the
// one named after the file, else the last, that defines both init and
// step. jobs bounds the entries surveyed at once (quint's own admission
// bounds its processes). An entry it could not survey (a tool failure)
// is an error of the list, its verdict left empty.
func Survey(ctx context.Context, roots Roots, entries []Entry, texts map[string]string, tmp string, jobs int) ([]Entry, []error) {
	out := slices.Clone(entries)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
		sem  = make(chan struct{}, max(jobs, 1))
	)
	for i := range out {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := survey(ctx, roots, &out[i], texts[out[i].ID], tmp); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", out[i].ID, err))
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return out, errs
}

func survey(ctx context.Context, roots Roots, e *Entry, text, tmp string) error {
	dir, err := os.MkdirTemp(tmp, "survey-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	p, err := e.Program(roots, text, dir)
	if err != nil {
		return err
	}
	p.Main = "" // typecheck first: it says which module compiles
	o, err := Quint{}.Run(ctx, p, dir)
	if err != nil {
		return err
	}
	errs, err := Errors(o.Typecheck, canon.Typecheck, o.Root)
	if err != nil {
		return err
	}
	e.Quint = Verdict{Typecheck: orNone(Codes(errs))}
	if e.Main == "" && e.Source != SourceInvariant && len(errs) == 0 {
		e.Main = compilable(o.Typecheck, filepath.Base(e.Path))
	}
	if e.Main != "" {
		p.Main, p.Init, p.Step, p.Invariants = e.Main, e.Init, e.Step, e.Invariants
		cdir, err := os.MkdirTemp(dir, "compile-")
		if err != nil {
			return err
		}
		o, err := Quint{}.Run(ctx, p, cdir)
		if err != nil {
			return err
		}
		v, err := VerdictOf(o)
		if err != nil {
			return err
		}
		e.Quint.Compile = v.Compile
	}
	e.Expect = expectOf(*e)
	return nil
}

func orNone(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// compilable is the module of a typecheck output that compile can take as
// main with the default init and step: the one named after file, else
// the last defining both ("" if none).
func compilable(typecheck []byte, file string) string {
	var doc struct {
		Modules []struct {
			Name         string `json:"name"`
			Declarations []struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"declarations"`
		} `json:"modules"`
	}
	if json.Unmarshal(typecheck, &doc) != nil {
		return ""
	}
	stem := strings.TrimSuffix(file, filepath.Ext(file))
	best := ""
	for _, m := range doc.Modules {
		var init, step bool
		for _, d := range m.Declarations {
			init = init || (d.Kind == "def" && d.Name == "init")
			step = step || (d.Kind == "def" && d.Name == "step")
		}
		if init && step {
			if m.Name == stem {
				return m.Name
			}
			best = m.Name
		}
	}
	return best
}

// Check compares two corpora's entries by id: those of want missing from
// got, those of got missing from want, and those whose text (sha256) or
// constructs differ, each as a line naming the entry.
func Check(want, got []Entry) []string {
	byID := map[string]Entry{}
	for _, e := range got {
		byID[e.ID] = e
	}
	var out []string
	seen := map[string]bool{}
	for _, w := range want {
		seen[w.ID] = true
		g, ok := byID[w.ID]
		switch {
		case !ok:
			out = append(out, w.ID+": in the manifest, not found")
		case g.SHA256 != w.SHA256:
			out = append(out, fmt.Sprintf("%s: its text changed (sha256 %s, pinned %s)", w.ID, g.SHA256, w.SHA256))
		case !slices.Equal(g.Constructs, w.Constructs):
			out = append(out, fmt.Sprintf("%s: its constructs are %v, pinned %v", w.ID, g.Constructs, w.Constructs))
		}
	}
	for _, g := range got {
		if !seen[g.ID] {
			out = append(out, g.ID+": found, not in the manifest")
		}
	}
	return out
}
