package connect

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/itf"
)

// ReadTraces reads the traces quint wrote for c (GenerateRun's files, with
// the Config it returned), in order. quint 0.33's Rust backend writes an
// integer of 16 characters or more as a bignumber.js object, which only
// its type tells from a record of fields s, e and c: at the first trace
// that holds one, the model (c.Spec's c.Main, else the module named as
// its file) is typechecked, once, and its types read every trace
// (binding.Model.Schema, itf.ReadFileWith).
func ReadTraces(c Config, files []string) ([]*itf.Trace, error) {
	r := &traceReader{c: c}
	out := make([]*itf.Trace, 0, len(files))
	for _, f := range files {
		tr, err := r.read(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		out = append(out, tr)
	}
	return out, nil
}

// traceReader reads one Config's traces, typechecking its model at the
// first that needs its types.
type traceReader struct {
	c Config
	r *binding.TraceReader
}

func (r *traceReader) read(path string) (*itf.Trace, error) {
	if r.r == nil {
		module := r.c.Main
		if module == "" { // quint's default main module: the file's name
			module = strings.TrimSuffix(filepath.Base(r.c.Spec), ".qnt")
		}
		r.r = &binding.TraceReader{Quint: r.c.Quint, Spec: r.c.Spec, Module: module}
	}
	return r.r.Read(path)
}
