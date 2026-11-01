package main

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"text/template"

	"github.com/chucklehead-dev/gotogether/internal/projectsetup"
	"github.com/chucklehead-dev/gotogether/internal/quintnames"
	"github.com/chucklehead-dev/gotogether/schemas"
)

// initData are the projects `gotogether init` writes, one directory per
// kind, each file a template over initVars (.tmpl dropped from its name;
// service.go.tmpl and model/model.qnt.tmpl named after the service).
//
//go:embed initdata
var initData embed.FS

// initKinds are the kinds of service init writes, and what to read for
// those it does not yet.
type initKind struct {
	unavailable, summary string
	mutant, line, bug    string
}

// initKinds is the project catalog and each source contrast. Read-only.
var initKinds = map[string]initKind{
	"http":    {summary: "an HTTP service, its model and binding, a simulated and a real-socket test, a mutant", mutant: "offByOne", line: offByOneLine, bug: offByOneBug},
	"library": {summary: "a Go library with immutable snapshots, its model and binding, observed-call tests and a snapshot-aliasing mutant", mutant: "aliasSnapshot", line: "return Snapshot{values: maps.Clone(s.values)}", bug: "return Snapshot{values: s.values} // the bug: the snapshot aliases mutable state"},
	"grpc":    {unavailable: "not written by init yet: start from examples/grpcledger (simgrpc serves a generated gRPC service in qenv)"},
	"queue":   {unavailable: "not written by init yet: start from examples/streams (a consumer and an outbox on Kafka, Kinesis or a simulated log)"},
}

// initVars are what a project's templates see.
type initVars struct {
	Name, Module string
	// SchemaURL is the binding's schema, which an editor's YAML language
	// server reads from the binding's first line
	SchemaURL string
}

// serviceName is a name init takes: a Go package's, which is also the
// model's module and the simulated server's host.
var serviceName = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// offByOne is the planted bug of the HTTP project's mutant: the line of
// its handler, and the mutant's, so that the mutant is a copy of the
// written file with that line alone changed.
const (
	offByOneLine = "if left >= qty {"
	offByOneBug  = "if left > qty { // the bug: the last unit of a sku is refused"
)

// runInit writes a new project: `gotogether init -kind http|library NAME`.
func runInit(stdout, stderr io.Writer, args []string) error {
	fset := commandFlags("init")
	kind := fset.String("kind", "http", "the project kind: http or library (grpc and queue name an example to start from)")
	dir := fset.String("dir", "", "where to write the project (default ./NAME; it must be empty or absent)")
	module := fset.String("module", "", "its module path (default example.com/NAME)")
	replace := fset.String("replace", "", "require gotogether from a checkout at this path (resolved against the current directory), not at this CLI's version")
	noTidy := fset.Bool("no-tidy", false, "write the files only: skip go get and go mod tidy (which download the toolkit)")
	fset.Parse(args)
	if fset.NArg() != 1 {
		return needs("init", "a project's name")
	}
	name := fset.Arg(0)
	if choice, ok := initKinds[*kind]; !ok {
		return fmt.Errorf("init -kind %s: the kinds are http, library, grpc and queue", *kind)
	} else if choice.unavailable != "" {
		return fmt.Errorf("init -kind %s: %s", *kind, choice.unavailable)
	}
	if err := checkServiceName(name); err != nil {
		return err
	}
	if *dir == "" {
		*dir = name
	}
	if *module == "" {
		*module = "example.com/" + name
	}
	if err := projectsetup.CheckModule(*module); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	if err := projectsetup.CheckDestination(*dir); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	if !*noTidy {
		if err := projectsetup.CheckGo(); err != nil {
			return fmt.Errorf("init: %w", err)
		}
	}
	if *replace != "" {
		if _, err := os.Stat(filepath.Join(*replace, "go.mod")); err != nil {
			return fmt.Errorf("init: -replace %s: no go.mod there: give the gotogether checkout's root (resolved against the current directory)", *replace)
		}
	}
	if err := writeProject(*dir, *kind, initVars{Name: name, Module: *module, SchemaURL: schemas.BindingURL}); err != nil {
		return err
	}
	if err := writeInitGoMod(*dir, *module, *replace); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s (module %s): %s\n", *dir, *module, initKinds[*kind].summary)
	plan := projectsetup.Plan{Label: "init", Dir: *dir}
	if *replace == "" {
		plan.Steps = append(plan.Steps, []string{"get", "github.com/chucklehead-dev/gotogether@" + cliVersion()})
	}
	plan.Steps = append(plan.Steps, []string{"mod", "tidy"})
	if *noTidy {
		fmt.Fprintf(stdout, "next: %s\n", plan.Resume(0))
		return nil
	}
	if *replace == "" {
		fmt.Fprintf(stdout, "downloading the toolkit (gotogether at %s; the first time takes a minute or two)\n", cliVersion())
	}
	if err := plan.Run(stderr); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "next: %s   (with quint on PATH, about 15 s: each test's summary shown)\n", projectsetup.Verify(*dir))
	return nil
}

// checkServiceName refuses a name no package, model module or host could
// have, saying what would do.
func checkServiceName(name string) error {
	switch {
	case !serviceName.MatchString(name):
		return fmt.Errorf("init: %q: name the service with lower-case letters and digits, a letter first (stock, orders2): it is the Go package's name and the model's module", name)
	case token.IsKeyword(name) || name == "main":
		return fmt.Errorf("init: %q is Go's own (a keyword, or main's package): name the service otherwise", name)
	case quintnames.Reserved(name):
		return fmt.Errorf("init: %q is a name quint keeps for a built-in (QNT101), and the model's module would take it: name the service otherwise", name)
	}
	return nil
}

// writeProject renders kind's templates into dir, then the mutant: a
// copy of the implementation file with one line changed.
func writeProject(dir, kind string, v initVars) error {
	root := path.Join("initdata", kind)
	var service []byte
	err := fs.WalkDir(initData, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		src, err := initData.ReadFile(p)
		if err != nil {
			return err
		}
		t, err := template.New(p).Parse(string(src))
		if err != nil {
			return fmt.Errorf("init: %s: %w", p, err)
		}
		var b bytes.Buffer
		if err := t.Execute(&b, v); err != nil {
			return fmt.Errorf("init: %s: %w", p, err)
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, root+"/"), ".tmpl")
		switch rel {
		case "service.go", "library.go":
			rel = v.Name + ".go"
			service = b.Bytes()
		case "model/model.qnt":
			rel = "model/" + v.Name + ".qnt"
		case "binding.yaml":
			rel = v.Name + ".binding.yaml"
		}
		out := b.Bytes()
		if strings.HasSuffix(rel, ".go") {
			if out, err = format.Source(out); err != nil {
				return fmt.Errorf("init: %s does not parse: %w", rel, err) // the template's own bug
			}
		}
		return writeNew(filepath.Join(dir, filepath.FromSlash(rel)), out, strings.HasSuffix(rel, ".sh"))
	})
	if err != nil {
		return err
	}
	if service == nil {
		return nil
	}
	contrast := initKinds[kind]
	if bytes.Count(service, []byte(contrast.line)) != 1 {
		return fmt.Errorf("init: the implementation template has no single %q for its mutant", contrast.line) // the template's own bug
	}
	mutant := bytes.Replace(service, []byte(contrast.line), []byte(contrast.bug), 1)
	if mutant, err = format.Source(mutant); err != nil {
		return fmt.Errorf("init: the mutant does not parse: %w", err)
	}
	return writeNew(filepath.Join(dir, "testdata", "mutants", contrast.mutant, v.Name+".go"), mutant, false)
}

// writeNew writes a new file, its directories made.
func writeNew(p string, data []byte, exec bool) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	mode := os.FileMode(0o644)
	if exec {
		mode = 0o755
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("init: %w", err)
	}
	return f.Close()
}

// writeInitGoMod writes the project's go.mod: the go version the toolkit
// needs, and with replace the core from that checkout, written relative
// to the module (as an example's replace => ../..).
func writeInitGoMod(dir, module, replace string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "module %s\n\ngo 1.26.1\n", projectsetup.ModulePath(module))
	if replace != "" {
		abs, err := filepath.Abs(replace)
		if err != nil {
			return err
		}
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		rel := abs
		if r, err := filepath.Rel(absDir, abs); err == nil {
			if rel = filepath.ToSlash(r); !strings.HasPrefix(rel, ".") {
				rel = "./" + rel
			}
		}
		fmt.Fprintf(&b, "\nrequire github.com/chucklehead-dev/gotogether v0.0.0\n\nreplace github.com/chucklehead-dev/gotogether => %s\n", projectsetup.ModulePath(rel))
	}
	return writeNew(filepath.Join(dir, "go.mod"), []byte(b.String()), false)
}

// cliVersion is the version of the toolkit this CLI was built from (go
// install …@v), else latest.
func cliVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "latest"
}
