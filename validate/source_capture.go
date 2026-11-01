package validate

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"

	"github.com/chucklehead-dev/gotogether/binding"
)

// sourceCapture guards the source inventory used by a checker, including
// conservative sibling dependencies used by the Quint cache. Quint still reads
// source files itself: boundary checks detect persistent edits, not an atomic
// filesystem snapshot (an edit restored while Quint runs cannot be detected).
type sourceCapture struct {
	files         []File
	model         RunModel
	bindingConfig string
}

func captureSource(b *binding.Binding) (*sourceCapture, error) {
	if len(importPaths(b.Defs)) > 0 {
		return nil, fmt.Errorf("binding %s imports files from defs, whose dependencies cannot be captured: move file imports into the model", b.Path)
	}
	m := &Manifest{}
	if err := m.setBinding(b); err != nil {
		return nil, err
	}
	files, ok := specFilesAll(b.SpecPath())
	if !ok {
		return nil, fmt.Errorf("capture model sources %s: an imported source cannot be read", b.SpecPath())
	}
	config, err := sourceBindingConfig(b)
	if err != nil {
		return nil, err
	}
	return &sourceCapture{files: files, model: m.Model, bindingConfig: config}, nil
}

func checkSourceFiles(expected []File, spec string) error {
	current, ok := specFilesAll(spec)
	if !ok {
		return fmt.Errorf("model source %s changed or cannot be read: restore the sources or create a new checker", spec)
	}
	if !reflect.DeepEqual(expected, current) {
		now := map[string]string{}
		for _, f := range current {
			now[f.Path] = f.SHA256
		}
		for _, f := range expected {
			if now[f.Path] != f.SHA256 {
				return fmt.Errorf("model source %s changed since it was loaded: restore the sources or create a new checker", f.Path)
			}
			delete(now, f.Path)
		}
		for path := range now {
			return fmt.Errorf("model source %s changed the source inventory: create a new checker", path)
		}
	}
	return nil
}

func (c *Checker) verifySource() error {
	if c.source == nil {
		return nil
	} // compiled and explicitly hand-built checkers
	if len(importPaths(c.Binding.Defs)) > 0 {
		return fmt.Errorf("binding %s imports files from defs: move file imports into the model", c.Binding.Path)
	}
	if err := verifyCapturedModel(&c.source.model); err != nil {
		return err
	}
	b := c.Binding
	config, err := sourceBindingConfig(b)
	if err != nil {
		return err
	}
	if config != c.source.bindingConfig {
		return fmt.Errorf("binding %s execution configuration changed since it was loaded: use WithConstants or WithInvariants for supported overrides, or load a new checker", b.Path)
	}
	if b.SpecPath() != c.source.model.Spec.Path || b.Module != c.source.model.Module {
		return fmt.Errorf("model source %s or module changed since the checker was loaded: create a new checker", c.source.model.Spec.Path)
	}
	f, err := hashed(b.Path)
	if err != nil {
		return err
	}
	if c.source.model.Binding == nil || f.Path != c.source.model.Binding.Path || f.SHA256 != c.source.model.Binding.SHA256 {
		return fmt.Errorf("binding %s changed since it was loaded: restore the binding or create a new checker", b.Path)
	}
	return checkSourceFiles(c.source.files, b.SpecPath())
}

func (c *Checker) captureReport(rep *Report) {
	if c.source == nil || rep == nil {
		return
	}
	model := c.source.model
	model.Constants = maps.Clone(c.Binding.Constants)
	model.Invariants = append([]string{}, c.Binding.Invariants...)
	for _, r := range rep.Actors {
		if r != nil {
			r.sourceModel = cloneRunModel(&model)
			r.sourceFiles = append([]File{}, c.source.files...)
		}
	}
}

func cloneRunModel(m *RunModel) *RunModel {
	if m == nil {
		return nil
	}
	// RunModel is the manifest value; JSON copying also prevents callers from
	// mutating its File pointers, slices and maps through SourceModel.
	data, _ := json.Marshal(m)
	var out RunModel
	_ = json.Unmarshal(data, &out)
	return &out
}

// SourceModel returns the source instance captured for this judgment. It is
// nil for compiled results and results built without source capture. The caller
// owns the returned copy; manifest creation checks the private captured value.
func (r *Result) SourceModel() *RunModel { return cloneRunModel(r.sourceModel) }

func sameSourceModel(a, b *RunModel) bool {
	// Convert belongs to the run's input conversion, not its model judgment.
	ac, bc := cloneRunModel(a), cloneRunModel(b)
	if ac == nil || bc == nil {
		return ac == nil && bc == nil
	}
	ac.Convert, bc.Convert = nil, nil
	return reflect.DeepEqual(ac, bc)
}

func (m *Manifest) matchSourceResults(results []*Result, inputs []RunInput) error {
	for _, r := range results {
		if r == nil || r.sourceModel == nil {
			continue
		} // low-level caller owns provenance
		expected := r.sourceModel
		if len(r.sourceFiles) != 0 {
			if err := checkSourceFiles(r.sourceFiles, expected.Spec.Path); err != nil {
				return err
			}
		}
		if err := verifyCapturedModel(expected); err != nil {
			return err
		}
		if sameSourceModel(expected, &m.Model) {
			continue
		}
		matched := false
		for _, in := range inputs {
			if sameSourceModel(expected, in.Model) {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		path := "model"
		if expected.Binding != nil {
			path = expected.Binding.Path
		}
		if expected.Spec != nil {
			path = expected.Spec.Path
		}
		return fmt.Errorf("manifest: source model %s changed since judgment (binding, spec, imports or effective instance): restore the captured files and instance or validate anew", path)
	}
	return nil
}

func verifyCapturedModel(m *RunModel) error {
	files := append([]File{}, m.Imports...)
	if m.Binding != nil {
		files = append(files, *m.Binding)
	}
	if m.Spec != nil {
		files = append(files, *m.Spec)
	}
	for _, expected := range files {
		current, err := hashed(expected.Path)
		if err != nil {
			return err
		}
		if current.SHA256 != expected.SHA256 {
			return fmt.Errorf("manifest: source %s changed since judgment: restore the captured source or validate anew", expected.Path)
		}
	}
	if m.Spec != nil {
		_, imports, err := specFiles(m.Spec.Path)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(imports, m.Imports) {
			return fmt.Errorf("manifest: source imports of %s changed since judgment: validate anew", m.Spec.Path)
		}
	}
	return nil
}

// VerifySource checks that a source checker's loaded binding and source inventory
// are still present unchanged. Compiled and hand-built checkers have no source
// inventory to verify. It detects persistent edits at this boundary; it does not
// provide an atomic snapshot of files read by external tools.
func (c *Checker) VerifySource() error { return c.verifySource() }

func sourceBindingConfig(b *binding.Binding) (string, error) {
	effective := *b
	// These two explicit Checker override APIs preserve source identity while
	// recording the effective values separately in RunModel.
	effective.Constants, effective.Invariants = nil, nil
	data, err := json.Marshal(&effective)
	return string(data), err
}
