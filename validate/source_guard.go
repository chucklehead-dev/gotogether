package validate

import "fmt"

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
