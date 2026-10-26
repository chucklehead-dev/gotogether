package validate

import (
	"fmt"
	"strings"
	"sync"

	"github.com/chucklehead-dev/gotogether/binding"
)

// models caches typechecked models by content: quint, the module, and
// the hashes of the spec and every file it imports (specFiles). A model
// is read-only once parsed, and a typecheck is most of a short check's
// time (a quint process of about a second): the CLI's tests, and a
// program validating many runs of one model, load it once.
// Process-wide.
var models sync.Map

// loadModel is binding.LoadModel through the content cache: the same
// files give the same model, a changed one (or a changed import) another.
func loadModel(quint, spec, module string) (*binding.Model, error) {
	f, imports, err := specFiles(spec)
	if err != nil {
		return binding.LoadModel(quint, spec, module) // its error says what is wrong
	}
	var key strings.Builder
	fmt.Fprintf(&key, "%s|%s|%s|%s", quint, module, f.Path, f.SHA256)
	for _, i := range imports {
		fmt.Fprintf(&key, "|%s=%s", i.Path, i.SHA256)
	}
	if m, ok := models.Load(key.String()); ok {
		return m.(*binding.Model), nil
	}
	m, err := binding.LoadModel(quint, spec, module)
	if err != nil {
		return nil, err
	}
	models.Store(key.String(), m)
	return m, nil
}
