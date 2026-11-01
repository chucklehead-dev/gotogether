// Command grist starts a grist application: `grist new <name>` writes a
// module (an App with one event per fact, a keyed read model, a command
// with a declared refusal and a rule guarding its event with another, a
// query and a processor that emits; its binary, its model's rules, a
// gristsim test with Expect claims, a mutant and the rule's generated
// one, a real-tier test, run-demo.sh and a README), then runs `go mod tidy`
// and the app's own `model` command, which generates the model's skeleton
// and both bindings from the App's catalog. `go test -short -v ./...` in
// the new module passes on its first run (quint on PATH), in about a
// minute.
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
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"text/template"

	"github.com/chucklehead-dev/gotogether/grist/internal/catalog"
	"github.com/chucklehead-dev/gotogether/internal/projectsetup"
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
  -replace REPO  require gotogether and grist from a checkout at REPO (resolved against the
                 current directory; go.mod's replace says it relative to the module, as
                 an example's ../..), not at the version this grist was built from
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
	q, err := catalog.QuintName(name)
	if err != nil {
		return usage(fmt.Sprintf("grist new: %q: %v", name, err))
	}
	if q == "grist" {
		// its model would be model/grist.qnt, the vendored library's file
		return usage(fmt.Sprintf("grist new: %q is grist's own library's name (model/grist.qnt): name the app otherwise", name))
	}
	if *dir == "" {
		*dir = name
	}
	if *module == "" {
		*module = "example.com/" + name
	}
	if err := projectsetup.CheckModule(*module); err != nil {
		return usage(fmt.Sprintf("grist new: %v", err))
	}
	if err := projectsetup.CheckDestination(*dir); err != nil {
		return fmt.Errorf("grist new: %w", err)
	}
	if !*noGenerate {
		if err := projectsetup.CheckGo(); err != nil {
			return fmt.Errorf("grist new: %w", err)
		}
	}
	if *replace != "" {
		// checked before anything is written: a bad path once left a
		// half-written module behind, failing in go's words
		for _, m := range []string{"go.mod", filepath.Join("grist", "go.mod")} {
			if _, err := os.Stat(filepath.Join(*replace, m)); err != nil {
				return usage(fmt.Sprintf("grist new: -replace %s: no %s there: give the gotogether checkout's root (resolved against the current directory)", *replace, m))
			}
		}
	}
	version := selfVersion()
	plan := projectsetup.Plan{Label: "grist new", Dir: *dir}
	if *replace == "" {
		plan.Steps = append(plan.Steps, []string{"get", "github.com/chucklehead-dev/gotogether/grist@" + version})
	}
	plan.Steps = append(plan.Steps, []string{"mod", "tidy"}, []string{"run", "./cmd/" + name, "model"})
	data := struct{ Name, Pkg, Q, Module string }{name, strings.ReplaceAll(name, "-", ""), q, *module}
	if err := write(*dir, data); err != nil {
		return err
	}
	if err := writeGoMod(*dir, data.Module, *replace); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s (module %s)\n", *dir, *module)
	if *noGenerate {
		fmt.Fprintf(stdout, "next: %s\n%s", plan.Resume(0), firstRun)
		return nil
	}
	if *replace == "" {
		fmt.Fprintf(stdout, "downloading the toolkit (grist and gotogether at %s; the first run takes about 2 minutes)\n", version)
	}
	if err := plan.Run(stderr); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "generated the model's skeleton and bindings (go run ./cmd/%s model)\nnext: %s\n%s", name, projectsetup.Verify(*dir), firstRun)
	return nil
}

// firstRun is what the first test run costs, said where it is suggested:
// the whole suite (Hegel's shrinks among it) ran 343 s with no output,
// where -short runs every claim in about a minute and -v shows each
// exploration's summary as it ends.
const firstRun = "  (about a minute with quint on PATH, each test's summary shown; without -short, Hegel's shrinks too: about 6 minutes)\n"

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
		rel = strings.Replace(rel, "model/app", "model/"+name, 1) // app.qnt, app_rules.qnt
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
	fmt.Fprintf(&b, "module %s\n\ngo 1.26.1\n", projectsetup.ModulePath(module))
	if replace != "" {
		// relative to the module, as an example's `replace => ../..`: the
		// go.mod stays right when the checkout moves with it
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
			rel = filepath.ToSlash(r)
			if !strings.HasPrefix(rel, ".") {
				rel = "./" + rel
			}
		}
		fmt.Fprintf(&b, "\nrequire (\n\tgithub.com/chucklehead-dev/gotogether v0.0.0\n\tgithub.com/chucklehead-dev/gotogether/grist v0.0.0\n)\n")
		fmt.Fprintf(&b, "\nreplace (\n\tgithub.com/chucklehead-dev/gotogether => %s\n\tgithub.com/chucklehead-dev/gotogether/grist => %s\n)\n", projectsetup.ModulePath(rel), projectsetup.ModulePath(filepath.ToSlash(filepath.Join(rel, "grist"))))
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
