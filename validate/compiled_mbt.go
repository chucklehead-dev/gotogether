package validate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/mbtmodel"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
)

// An MBTModel is a spec's main module compiled for connect
// (connect.MBTModel, the same type): CompileMBT writes it.
type MBTModel = mbtmodel.Model

// MBTOptions say which module of which spec CompileMBT compiles, as
// connect.Config names them: Spec, Main (default the file's module), Init
// and Step (default init and step), and the quint that compiles it
// (default quint).
type MBTOptions struct {
	Spec, Main, Init, Step, Quint string
}

// CompileMBT compiles c's spec (its c.Main, else the file's module) for
// connect where quint is not installed (connect.MBTModel, DESIGN §109):
// quint's IR of the main module with every definition its scope names
// kept (quint's flattening drops what init and step do not use, which
// would leave Config.Require and Step.Eval without the vals they name),
// and the spec's and its imports' hashes, paths relative to out's
// directory (where the artifact is written: connect.CompiledFor by
// convention). It needs quint (c.Quint, else quint).
func CompileMBT(ctx context.Context, c MBTOptions, out string) (*MBTModel, error) {
	quint := c.Quint
	if quint == "" {
		quint = "quint"
	}
	qv := quintVersion(ctx, quint)
	if qv == "" {
		return nil, fmt.Errorf("compile: %s --version failed: compiling needs quint (npm i -g @informalsystems/quint; gotogether doctor says what is installed)", quint)
	}
	spec, err := filepath.Abs(c.Spec)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "gotogether-compile-mbt-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	tc := filepath.Join(tmp, "typecheck.json")
	var stderr bytes.Buffer
	cmd := quintcmd.Command(ctx, quint, filepath.Dir(spec), "typecheck", spec, "--out", tc)
	cmd.Stdout, cmd.Stderr = &stderr, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("compile: quint typecheck %s: %w: %s", c.Spec, err, strings.TrimSpace(stderr.String()))
	}
	data, err := os.ReadFile(tc)
	if err != nil {
		return nil, err
	}
	main := c.Main
	if main == "" {
		main = strings.TrimSuffix(filepath.Base(spec), ".qnt")
	}
	keep, err := scopeNames(data, main)
	if err != nil {
		return nil, fmt.Errorf("compile: %s: %w", c.Spec, err)
	}
	var ir []byte
	for {
		ir, err = compileKeeping(ctx, quint, spec, c, keep, tmp)
		if nf, ok := err.(*notKept); ok && slices.Contains(keep, nf.name) {
			// a name the typecheck lists that quint's scope does not
			// resolve unqualified (an import's private one): left out
			keep = slices.DeleteFunc(keep, func(n string) bool { return n == nf.name })
			continue
		}
		if err != nil {
			return nil, err
		}
		break
	}
	if _, err := qeval.Load(ir); err != nil {
		return nil, fmt.Errorf("compile: the Go evaluator cannot read quint's IR of %s: %w", c.Spec, err)
	}
	dir, err := filepath.Abs(filepath.Dir(out))
	if err != nil {
		return nil, err
	}
	f, imports, err := specFiles(spec)
	if err != nil {
		return nil, err
	}
	var sources []mbtmodel.Source
	for _, s := range append([]File{*f}, imports...) {
		rel, err := filepath.Rel(dir, s.Path)
		if err != nil {
			return nil, err
		}
		sources = append(sources, mbtmodel.Source{Path: filepath.ToSlash(rel), SHA256: s.SHA256})
	}
	rel, err := filepath.Rel(dir, spec)
	if err != nil {
		return nil, err
	}
	return &mbtmodel.Model{Format: mbtmodel.Format, Quint: qv, Gotogether: coreVersion(), Spec: filepath.ToSlash(rel), Main: c.Main,
		Sources: sources, IR: json.RawMessage(bytes.TrimSpace(ir))}, nil
}

// notKept is quint's refusal of a name compileKeeping asked it to keep.
type notKept struct{ name string }

func (e *notKept) Error() string { return "quint finds no definition " + e.name }

var quintNameNotFound = regexp.MustCompile(`QNT404\] Name '([^']+)' not found`)

// compileKeeping is quint compile's JSON IR of c's main module, keeping
// the definitions keep names (as --invariant: any definition is kept so).
func compileKeeping(ctx context.Context, quint, spec string, c MBTOptions, keep []string, tmp string) ([]byte, error) {
	args := []string{"compile", "--target", "json", "--verbosity", "0"}
	if c.Main != "" {
		args = append(args, "--main", c.Main)
	}
	if c.Init != "" {
		args = append(args, "--init", c.Init)
	}
	if c.Step != "" {
		args = append(args, "--step", c.Step)
	}
	if len(keep) > 0 {
		args = append(args, "--invariant", strings.Join(keep, ","))
	}
	// stdout to a file: a pipe loses what quint has not flushed when it exits
	f, err := os.Create(filepath.Join(tmp, "ir.json"))
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd := quintcmd.Command(ctx, quint, filepath.Dir(spec), append(args, spec)...)
	cmd.Stdout, cmd.Stderr = f, &stderr
	err = cmd.Run()
	f.Close()
	if err != nil {
		if m := quintNameNotFound.FindStringSubmatch(stderr.String()); m != nil {
			return nil, &notKept{m[1]}
		}
		return nil, fmt.Errorf("compile: quint compile %s: %w: %s", c.Spec, err, strings.TrimSpace(stderr.String()))
	}
	return os.ReadFile(f.Name())
}

// scopeNames are the definitions module main's scope names unqualified,
// from quint's typecheck JSON: its own (runs and temporal properties
// aside) and, through each instance or import of a whole module, that
// module's, transitively.
func scopeNames(tc []byte, main string) ([]string, error) {
	var out struct {
		Modules []struct {
			Name         string `json:"name"`
			Declarations []struct {
				Kind          string `json:"kind"`
				Name          string `json:"name"`
				Qualifier     string `json:"qualifier"`
				ProtoName     string `json:"protoName"`
				DefName       string `json:"defName"`
				QualifiedName string `json:"qualifiedName"`
			} `json:"declarations"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(tc, &out); err != nil {
		return nil, err
	}
	byName := map[string]int{}
	for i, m := range out.Modules {
		byName[m.Name] = i
	}
	if _, ok := byName[main]; !ok {
		return nil, fmt.Errorf("no module %s", main)
	}
	var names []string
	seen := map[string]bool{}
	var walk func(mod string)
	walk = func(mod string) {
		i, ok := byName[mod]
		if !ok || seen["module "+mod] {
			return
		}
		seen["module "+mod] = true
		for _, d := range out.Modules[i].Declarations {
			switch d.Kind {
			case "def":
				if d.Qualifier == "run" || d.Qualifier == "temporal" || strings.Contains(d.Name, "::") || seen[d.Name] {
					continue
				}
				seen[d.Name] = true
				names = append(names, d.Name)
			case "instance", "import", "export":
				if d.QualifiedName == "" && (d.DefName == "" || d.DefName == "*") {
					walk(d.ProtoName)
				}
			}
		}
	}
	walk(main)
	slices.Sort(names)
	return names, nil
}
