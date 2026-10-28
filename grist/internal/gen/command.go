package gen

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Command is an app's binary's model command (grist.Main's): Generate's
// files written under -dir (default .), or with -check compared with
// those there (an error naming each that differs); -init writes
// Starter's files where there are none. It lists the holes the app's
// model has not filled. usage makes the error of a command line it
// cannot take.
func Command(app App, args []string, stdout io.Writer, usage func(msg string) error) error {
	c := app.Catalog()
	fs := flag.NewFlagSet(c.Name+" model", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", ".", "")
	check := fs.Bool("check", false, "")
	starter := fs.Bool("init", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return usage(fmt.Sprintf("usage: %s model [-dir DIR] [-check] [-init]", c.Name))
	}
	own := filepath.Join(*dir, modelFile(c.Name))
	rules := filepath.Join(*dir, rulesFile(c.Name))
	if *starter && !*check {
		// an old layout's model is moved by hand, never stubbed beside
		if b, err := readOwn(*dir, modelFile(c.Name)); err != nil {
			return err
		} else if err := oldLayout(c.Name, mustQuint(c.Name), b); err != nil {
			return err
		}
		files, err := Starter(app)
		if err != nil {
			return err
		}
		for _, rel := range []string{rulesFile(c.Name), modelFile(c.Name)} {
			path := filepath.Join(*dir, rel)
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, files[rel], 0o644); err != nil {
				return err
			}
			if rel == rulesFile(c.Name) {
				fmt.Fprintf(stdout, "wrote %s (every hole stubbed: write the rules)\n", path)
			} else {
				fmt.Fprintf(stdout, "wrote %s (no invariant yet: write them)\n", path)
			}
		}
	}
	g, err := Generate(app, *dir)
	if err != nil {
		return err
	}
	for _, w := range g.Warnings {
		fmt.Fprintf(stdout, "%s model: warning: %s\n", c.Name, w)
	}
	if *check {
		if d := g.Diff(*dir); len(d) > 0 {
			return fmt.Errorf("%s model: %s differ from what the catalog generates: regenerate with `%s model` and commit them", c.Name, strings.Join(d, ", "), c.Name)
		}
	} else if err := g.Write(*dir); err != nil {
		return err
	}
	if _, err := os.Stat(rules); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s model: there is no %s, the app's rules (every hole of the skeleton): `%s model -init` writes one with every hole stubbed", c.Name, rules, c.Name)
	}
	if _, err := os.Stat(own); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s model: there is no %s, the app's invariants (the modules its bindings name): `%s model -init` writes one with none yet", c.Name, own, c.Name)
	}
	if len(g.Missing) > 0 {
		var b strings.Builder
		for _, h := range g.Holes {
			if slices.Contains(g.Missing, h.Name) {
				fmt.Fprintf(&b, "\n%s  %s", comment("  ", h.Doc), h.Stub)
			}
		}
		return fmt.Errorf("%s model: %s does not define %s, which the skeleton calls; in module %sRules, for instance:%s",
			c.Name, rules, strings.Join(g.Missing, ", "), mustQuint(c.Name), b.String())
	}
	if !*check {
		fmt.Fprintf(stdout, "wrote %s under %s\n", strings.Join(g.paths(), ", "), *dir)
	}
	return nil
}

func mustQuint(name string) string { q, _ := QuintName(name); return q }
