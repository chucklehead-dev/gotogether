package validate

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"github.com/chucklehead-dev/gotogether/binding"
)

// checkEvaluation prevents an ambiguous recipe from silently falling back to
// Quint. Compiled manifests describe one primary binding, never ITF or searches.
func (m *Manifest) checkEvaluation() error {
	for _, in := range m.Inputs {
		if in.Model != nil && in.Model.Compiled != nil {
			return errors.New("manifest: compiled evaluation requires the primary binding, not an ITF input model")
		}
	}
	if m.Model.Compiled == nil {
		return nil
	}
	if m.Model.Binding == nil {
		return errors.New("manifest: a compiled model requires its recorded binding")
	}
	if m.Tools.Quint != "" || m.Tools.Backend != "" {
		return errors.New("manifest: compiled evaluation records no runtime Quint or backend; compilation provenance belongs to the hashed artifact")
	}
	if m.Check.Linearize || m.Check.Key != "" || m.Check.ResolvePending {
		return fmt.Errorf("manifest: compiled evaluation cannot replay history searches: %w", ErrNeedsQuint)
	}
	for _, in := range m.Inputs {
		if in.Format == "itf" || in.Model != nil {
			return fmt.Errorf("manifest: compiled evaluation cannot replay ITF inputs: %w", ErrNeedsQuint)
		}
	}
	return nil
}

func (m *Manifest) setCompiledBinding(b *binding.Binding, path string) error {
	if b == nil || b.Path == "" {
		return errors.New("manifest: compiled evaluation needs a file-loaded binding")
	}
	// Revalidate before hashing: stale or malformed artifacts are never exported
	// as an executable recipe merely because a caller supplies a path.
	checker, err := NewCompiledChecker(b.Path, path, Options{})
	if err != nil {
		return err
	}
	cm := checker.Compiled()
	if len(cm.Sources) == 0 || filepath.Clean(filepath.Join(b.Dir, filepath.FromSlash(cm.Sources[0].Path))) != filepath.Clean(b.SpecPath()) {
		return errors.New("manifest: compiled artifact does not record the binding's main source first: compile the complete model anew")
	}
	if _, err := checker.WithConstants(b.Constants); err != nil {
		return err
	}
	if _, err := checker.WithInvariants(b.Invariants...); err != nil {
		return err
	}
	if m.Model.Binding, err = hashed(b.Path); err != nil {
		return err
	}
	if m.Model.Binding.SHA256 != checker.Compiled().Binding.SHA256 {
		return errors.New("manifest: binding changed while compiled provenance was read: restore the binding used by the artifact")
	}
	if m.Model.Compiled, err = hashed(path); err != nil {
		return err
	}
	if m.Model.Compiled.SHA256 != checker.Compiled().digest {
		return errors.New("manifest: compiled artifact changed while its provenance was read: restore the judged artifact")
	}
	m.Model.Module = b.Module
	m.Model.Constants = maps.Clone(b.Constants)
	m.Model.Invariants = append([]string{}, b.Invariants...)
	for _, source := range checker.Compiled().Sources {
		p := filepath.Join(b.Dir, filepath.FromSlash(source.Path))
		f, err := hashed(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if f.SHA256 != source.SHA256 {
			return fmt.Errorf("manifest: compiled source %s changed while provenance was read: restore the source used by the artifact", p)
		}
		if filepath.Clean(p) == filepath.Clean(b.SpecPath()) {
			m.Model.Spec = f
		} else {
			m.Model.Imports = append(m.Model.Imports, *f)
		}
	}
	return nil
}

// compiledSources returns all provenance paths, including absent sources. Their
// locations matter to relocation even though only present sources are copied.
func (md *RunModel) compiledSources() (*CompiledModel, []File, error) {
	if md.Binding == nil || md.Compiled == nil {
		return nil, nil, errors.New("manifest: incomplete compiled binding/artifact metadata")
	}
	cm, err := LoadCompiled(md.Compiled.Path)
	if err != nil {
		return nil, nil, err
	}
	b, err := binding.Load(md.Binding.Path)
	if err != nil {
		return nil, nil, err
	}
	if len(cm.Sources) == 0 || filepath.Clean(filepath.Join(b.Dir, filepath.FromSlash(cm.Sources[0].Path))) != filepath.Clean(b.SpecPath()) {
		return nil, nil, errors.New("manifest: compiled artifact does not record the binding's main source first: compile the complete model anew")
	}
	var files []File
	seen := map[string]bool{}
	for _, source := range cm.Sources {
		p := filepath.Clean(filepath.Join(filepath.Dir(md.Binding.Path), filepath.FromSlash(source.Path)))
		if source.Path == "" || source.SHA256 == "" || seen[p] {
			return nil, nil, fmt.Errorf("manifest: compiled source %q has missing or duplicate provenance metadata", source.Path)
		}
		seen[p] = true
		files = append(files, File{Path: p, SHA256: source.SHA256})
	}
	if len(files) == 0 {
		return nil, nil, errors.New("manifest: compiled model records no source provenance: compile it again")
	}
	return cm, files, nil
}

func (md *RunModel) verifyCompiled() []error {
	_, sources, err := md.compiledSources()
	if err != nil {
		return []error{err}
	}
	var errs []error
	if checker, err := NewCompiledChecker(md.Binding.Path, md.Compiled.Path, Options{}); err != nil {
		errs = append(errs, err)
	} else {
		if md.Module != checker.Binding.Module {
			errs = append(errs, fmt.Errorf("manifest: recorded module %s differs from compiled binding module %s", md.Module, checker.Binding.Module))
		}
		if _, err := checker.WithConstants(md.Constants); err != nil {
			errs = append(errs, err)
		}
		if _, err := checker.WithInvariants(md.Invariants...); err != nil {
			errs = append(errs, err)
		}
	}
	recorded := map[string]File{}
	if md.Spec != nil {
		recorded[filepath.Clean(md.Spec.Path)] = *md.Spec
	}
	for _, f := range md.Imports {
		p := filepath.Clean(f.Path)
		if _, ok := recorded[p]; ok {
			errs = append(errs, fmt.Errorf("manifest: duplicate compiled source %s", p))
		}
		recorded[p] = f
	}
	for _, source := range sources {
		prior, wasPresent := recorded[source.Path]
		delete(recorded, source.Path)
		if wasPresent && prior.SHA256 != source.SHA256 {
			errs = append(errs, fmt.Errorf("manifest: compiled source %s has a recorded hash different from its artifact", source.Path))
		}
		now, err := hashFile(source.Path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if wasPresent {
				errs = append(errs, fmt.Errorf("the compiled model source %s was present when judged but is missing: restore the recorded source coverage or validate anew", source.Path))
			}
		case err != nil:
			errs = append(errs, fmt.Errorf("compiled model source %s: %w", source.Path, err))
		case !wasPresent:
			errs = append(errs, fmt.Errorf("the compiled model source %s was absent when judged but is now present: restore the recorded source coverage or validate anew", source.Path))
		case now != source.SHA256:
			errs = append(errs, fmt.Errorf("the compiled model source %s changed since the run was judged: restore it or validate anew", source.Path))
		}
	}
	for p := range recorded {
		errs = append(errs, fmt.Errorf("manifest: recorded source %s is absent from the compiled artifact's provenance", p))
	}
	return errs
}

func (md *RunModel) verifyCompiledBundle(root string) error {
	cm, sources, err := md.compiledSources()
	if err != nil {
		return err
	}
	b, err := binding.Load(md.Binding.Path)
	if err != nil {
		return err
	}
	if absoluteReference(b.Spec) {
		return fmt.Errorf("manifest bundle: binding %s has an absolute spec reference %q: use a relative spec before compiling", md.Binding.Path, b.Spec)
	}
	if len(importPaths(b.Defs)) > 0 {
		return fmt.Errorf("manifest bundle: binding %s imports files from defs, whose dependencies were not recorded: move file imports into the model and compile anew", md.Binding.Path)
	}
	if absoluteReference(cm.Binding.Path) || filepath.Clean(filepath.Join(filepath.Dir(md.Compiled.Path), filepath.FromSlash(cm.Binding.Path))) != filepath.Clean(md.Binding.Path) {
		return fmt.Errorf("manifest bundle: compiled artifact %s must refer relatively to its recorded binding %s: compile and write the artifact in the intended portable layout", md.Compiled.Path, md.Binding.Path)
	}
	for i, source := range cm.Sources {
		if absoluteReference(source.Path) {
			return fmt.Errorf("manifest bundle: compiled source reference %q is absolute: compile with a portable relative source layout", source.Path)
		}
		if root != "" && !within(root, sources[i].Path) {
			return fmt.Errorf("manifest bundle: compiled source path %s escapes the staged directory", sources[i].Path)
		}
	}
	// Present source files must still form the artifact's declared graph. Missing
	// files are deliberate deployment coverage, not a reason to invent a graph.
	graph := map[string]bool{}
	for _, f := range sources {
		graph[filepath.Clean(f.Path)] = true
	}
	present := append([]File{}, md.Imports...)
	if md.Spec != nil {
		present = append(present, *md.Spec)
	}
	for _, f := range present {
		data, err := os.ReadFile(f.Path)
		if err != nil {
			return err
		}
		for _, ref := range importPaths(string(data)) {
			if absoluteReference(ref) {
				return fmt.Errorf("manifest bundle: absolute import %q in %s: compile with portable relative imports", ref, f.Path)
			}
			p := filepath.Join(filepath.Dir(f.Path), filepath.FromSlash(ref))
			if filepath.Ext(p) != ".qnt" {
				p += ".qnt"
			}
			if !graph[filepath.Clean(p)] {
				return fmt.Errorf("manifest bundle: import %q in %s is absent from the compiled source graph: compile the complete model anew", ref, f.Path)
			}
		}
	}
	return nil
}

// matchCompiledResults keeps the recipe bound to what was actually loaded for
// the judgment, rather than a newly replaced artifact or changed source coverage.
func (m *Manifest) matchCompiledResults(results []*Result) error {
	current := map[string]string{}
	if m.Model.Spec != nil {
		current[filepath.Clean(m.Model.Spec.Path)] = m.Model.Spec.SHA256
	}
	for _, f := range m.Model.Imports {
		current[filepath.Clean(f.Path)] = f.SHA256
	}
	for _, r := range results {
		if r == nil {
			continue
		}
		if r.compiledHash != m.Model.Compiled.SHA256 {
			return errors.New("manifest: compiled artifact changed since the run was judged: restore the exact artifact used by the judgment or validate anew")
		}
		prior := map[string]string{}
		for _, f := range r.compiledSources {
			prior[filepath.Clean(f.Path)] = f.SHA256
		}
		if !maps.Equal(prior, current) {
			return errors.New("manifest: compiled source coverage changed since the run was judged: restore its present/absent sources or validate anew")
		}
	}
	return nil
}
