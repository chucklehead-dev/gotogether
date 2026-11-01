package validate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/memo"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/itf"
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
	files, ok := specFilesAll(spec)
	if !ok {
		return binding.LoadModel(quint, spec, module)
	}
	var key strings.Builder
	fmt.Fprintf(&key, "%s|%s", quint, module)
	for _, f := range files {
		fmt.Fprintf(&key, "|%s=%s", f.Path, f.SHA256)
	}

	return models.Do(key.String(), func() (*binding.Model, error) {
		m, err := typecheck(quint, spec, module)
		if err == nil {
			err = checkSourceFiles(files, spec)
		}
		return m, err
	})
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
	if current, valid := cacheKey(quint, "typecheck", spec); valid && current == key {
		cachePut("typecheck", key, data)
	} else {
		return nil, fmt.Errorf("model source %s changed during typecheck: validate anew", spec)
	}
	return m, nil
}

// readQuintITF reads the ITF trace at path that quint wrote replaying
// spec's module, by the model's types where it needs them: quint 0.33's
// Rust backend writes an integer of 16 characters or more as a
// bignumber.js object, which only its type tells from a record of fields
// s, e and c (itf.AmbiguousError). The model is typechecked only for a
// trace that holds one, through loadModel's cache.
func readQuintITF(quint, spec, module, path string) (*itf.Trace, error) {
	tr, err := itf.ReadFile(path)
	if _, ok := errors.AsType[*itf.AmbiguousError](err); !ok || spec == "" {
		return tr, err
	}
	m, merr := loadModel(quint, spec, module)
	if merr != nil {
		return nil, fmt.Errorf("%w (the model's types, which tell: %v)", err, merr)
	}
	return itf.ReadFileWith(path, m.Schema())
}
