// Command grist starts a grist application: `grist new <name>` writes a
// module (an App with one event per fact, a keyed read model, a command
// with two declared refusals, a query and a processor that emits; its
// binary, its model's rules, a gristsim test with Expect claims, a mutant,
// a real-tier test, run-demo.sh and a README), then runs `go mod tidy`
// and the app's own `model` command, which generates the model's skeleton
// and both bindings from the App's catalog. `go test ./...` in the new
// module passes on its first run (quint on PATH).
//
// The generator itself is the application's binary's (`<app> model`,
// grist.Main): it reads the App's catalog, which no other process can.
package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"text/template"

	"github.com/chucklehead-dev/gotogether/grist"
)

//go:embed all:scaffold
var scaffold embed.FS

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var u usage
		if errors.As(err, &u) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// usage is a command line run cannot take.
type usage string

func (u usage) Error() string { return string(u) }

const help = `usage: grist new [-dir DIR] [-module PATH] [-replace REPO] [-no-generate] NAME
  NAME           the app's name: lower-case words joined by dashes (its store's, its model's)
  -dir DIR       where to write the module (default ./NAME)
  -module PATH   its module path (default example.com/NAME)
  -replace REPO  require gotogether and grist from a checkout at REPO (a replace directive),
                 not at the version this grist was built from
  -no-generate   write the files only: skip go mod tidy and the app's model command`

// run runs grist's command line.
func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "new" {
		return usage(help)
	}
	fl := flag.NewFlagSet("grist new", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	dir := fl.String("dir", "", "")
	module := fl.String("module", "", "")
	replace := fl.String("replace", "", "")
	noGenerate := fl.Bool("no-generate", false, "")
	if err := fl.Parse(args[1:]); err != nil || fl.NArg() != 1 {
		return usage(help)
	}
	name := fl.Arg(0)
	if !appName.MatchString(name) {
		return usage(fmt.Sprintf("grist new: %q: name the app with lower-case words joined by dashes (inventory, item-store)\n%s", name, help))
	}
	q, err := grist.QuintName(name)
	if err != nil {
		return usage(fmt.Sprintf("grist new: %q: %v", name, err))
	}
	if *dir == "" {
		*dir = name
	}
	if *module == "" {
		*module = "example.com/" + name
	}
	if entries, err := os.ReadDir(*dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("grist new: %s exists and is not empty: give another -dir", *dir)
	}
	data := struct{ Name, Pkg, Q, Module string }{name, strings.ReplaceAll(name, "-", ""), q, *module}
	if err := write(*dir, data); err != nil {
		return err
	}
	if err := writeGoMod(*dir, data.Module, *replace); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s (module %s)\n", *dir, *module)
	if *noGenerate {
		fmt.Fprintf(stdout, "next: cd %s && go mod tidy && go run ./cmd/%s model && go test ./...\n", *dir, name)
		return nil
	}
	if *replace == "" {
		// grist and the core at the version this grist was built from
		if err := goCmd(*dir, stderr, "get", "github.com/chucklehead-dev/gotogether/grist@"+selfVersion()); err != nil {
			return err
		}
	}
	for _, c := range [][]string{{"mod", "tidy"}, {"run", "./cmd/" + name, "model"}} {
		if err := goCmd(*dir, stderr, c...); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "generated the model's skeleton and bindings (go run ./cmd/%s model)\nnext: cd %s && go test ./...\n", name, *dir)
	return nil
}

var appName = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// write renders the scaffold into dir: each .tmpl file at its path, the
// app's cmd and model files named after it.
func write(dir string, data any) error {
	return fs.WalkDir(scaffold, "scaffold", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := scaffold.ReadFile(path)
		if err != nil {
			return err
		}
		t, err := template.New(path).Delims("{{", "}}").Parse(string(b))
		if err != nil {
			return fmt.Errorf("grist new: %s: %w", path, err)
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(path, "scaffold/"), ".tmpl")
		name := data.(struct{ Name, Pkg, Q, Module string }).Name
		rel = strings.Replace(rel, "cmd/app/", "cmd/"+name+"/", 1)
		rel = strings.Replace(rel, "model/app.qnt", "model/"+name+".qnt", 1)
		out := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := t.Execute(f, data); err != nil {
			return fmt.Errorf("grist new: %s: %w", path, err)
		}
		if strings.HasSuffix(rel, ".sh") {
			return os.Chmod(out, 0o755)
		}
		return nil
	})
}

// writeGoMod writes the module's go.mod: the go version grist needs, and,
// with replace, the toolkit's two modules from that checkout.
func writeGoMod(dir, module, replace string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "module %s\n\ngo 1.26.1\n", module)
	if replace != "" {
		abs, err := filepath.Abs(replace)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "\nrequire (\n\tgithub.com/chucklehead-dev/gotogether v0.0.0\n\tgithub.com/chucklehead-dev/gotogether/grist v0.0.0\n)\n")
		fmt.Fprintf(&b, "\nreplace (\n\tgithub.com/chucklehead-dev/gotogether => %s\n\tgithub.com/chucklehead-dev/gotogether/grist => %s\n)\n", abs, filepath.Join(abs, "grist"))
	}
	return os.WriteFile(filepath.Join(dir, "go.mod"), []byte(b.String()), 0o644)
}

// selfVersion is the version of grist this binary was built from
// (go install ...@v), else latest.
func selfVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "latest"
}

// goCmd runs the go command in dir, its output to w, outside any
// workspace (the new module builds on its own go.mod).
func goCmd(dir string, w io.Writer, args ...string) error {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = w, w
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("grist new: go %s in %s: %w", strings.Join(args, " "), dir, err)
	}
	return nil
}
