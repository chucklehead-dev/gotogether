package validate

import (
	"fmt"
	"slices"
)

// SourceGuard retains a model's source inventory across operations that use its
// types. It detects observed file changes; it is not an atomic filesystem snapshot.
type SourceGuard struct {
	spec  string
	files []File
}

// CaptureModelSources records the model, its imports and conservative sibling
// dependencies before reading model-dependent data.
func CaptureModelSources(spec string) (*SourceGuard, error) {
	files, ok := specFilesAll(spec)
	if !ok {
		return nil, fmt.Errorf("capture model sources %s: an imported source cannot be read", spec)
	}
	return &SourceGuard{spec: spec, files: files}, nil
}

// Verify refuses source changes since capture. A nil guard has nothing to verify.
func (g *SourceGuard) Verify() error {
	if g == nil {
		return nil
	}
	return checkSourceFiles(g.files, g.spec)
}

// CapturedModelSources returns an owned guard of the original source inventory
// captured when c was loaded. It refuses changed sources instead of refreshing
// the inventory, so exporting a comparison cannot substitute newer model bytes.
// Compiled or hand-built checkers without captured model sources are refused.
func (c *Checker) CapturedModelSources() (*SourceGuard, error) {
	if c == nil || c.Binding == nil || c.source == nil || c.source.model.Spec == nil || c.source.model.Spec.Path == "" || len(c.source.files) == 0 {
		return nil, fmt.Errorf("model source capture: the checker has no captured model sources: load the model with NewChecker before exporting")
	}
	if err := c.VerifySource(); err != nil {
		return nil, err
	}
	return &SourceGuard{spec: c.source.model.Spec.Path, files: slices.Clone(c.source.files)}, nil
}
