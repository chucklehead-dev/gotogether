package validate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/memo"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
)

// models caches typechecked models by content: quint, the module, and
// the hashes of the spec and every file it imports (specFiles). A model
// is read-only once parsed, and a typecheck is most of a short check's
// time (a quint process of about a second): the CLI's tests, and a
// program validating many runs of one model, load it once.
// Process-wide.
var models memo.Map[string, *binding.Model]

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
	return models.Do(key.String(), func() (*binding.Model, error) { return typecheck(quint, spec, module) })
}

// typecheck is binding.LoadModel through the cache across processes
// (cache.go): quint's typecheck JSON of spec kept by its content, and
// parsed. A typecheck that fails is LoadModel's to report.
func typecheck(quint, spec, module string) (*binding.Model, error) {
	key, ok := cacheKey(quint, "typecheck", spec)
	if !ok {
		return binding.LoadModel(quint, spec, module)
	}
	if data, ok := cacheGet("typecheck", key); ok {
		if m, err := binding.ParseTypecheck(data, module); err == nil {
			return m, nil
		}
	}
	unlock, data, ok := cacheLock("typecheck", key)
	defer unlock()
	if ok {
		if m, err := binding.ParseTypecheck(data, module); err == nil {
			return m, nil // another process's, typechecked meanwhile
		}
	}
	if quint == "" {
		quint = "quint"
	}
	tmp, err := os.CreateTemp("", "gotogether-typecheck-*.json")
	if err != nil {
		return binding.LoadModel(quint, spec, module)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	cmd := quintcmd.Command(context.Background(), quint, filepath.Dir(spec), "typecheck", filepath.Base(spec), "--out", tmp.Name())
	if err := cmd.Run(); err != nil {
		return binding.LoadModel(quint, spec, module) // its error says what is wrong
	}
	data, err = os.ReadFile(tmp.Name())
	if err != nil {
		return binding.LoadModel(quint, spec, module)
	}
	m, err := binding.ParseTypecheck(data, module)
	if err != nil {
		return nil, err
	}
	cachePut("typecheck", key, data)
	return m, nil
}
