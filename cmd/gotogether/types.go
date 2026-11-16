package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/literate"
	"github.com/chucklehead-dev/gotogether/internal/typegen"
)

// types is the types command: the Go types of a model's types (ROADMAP
// CW5's static half, internal/typegen), written to -o or standard output,
// so that nobody writes Go mirrors of a model's records and sums by hand.
// With -check it writes nothing and fails (exit 1) when the file at -o is
// missing or stale, naming the types that changed and the command that
// writes it again: the check CI runs where the generated file is checked
// in. Exit 2 is a bad invocation or a model quint refuses.
func types(ctx context.Context, out io.Writer, args []string) (int, error) {
	fset := commandFlagsWithHandling("types", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	spec := fset.String("spec", "", "the model: a .qnt, or a literate model's .md (written to its .qnt, as tangle does)")
	main := fset.String("main", "", "the model's main module (default: the spec's file name, as quint's)")
	bpath := fset.String("binding", "", "take -spec and -main from this binding (its spec, and its module or the module its instance imports)")
	pkg := fset.String("package", "", "the generated file's package (default: that of the Go files in -o's directory, else model)")
	o := fset.String("o", "", "the generated file (default: standard output)")
	check := fset.Bool("check", false, "write nothing: fail if the file at -o is missing or not what the model generates now")
	quint := fset.String("quint", "quint", "quint binary")
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fset.SetOutput(out)
			fset.Usage()
			return 0, nil
		}
		return 2, fmt.Errorf("types: %v (gotogether types -h lists its flags)", err)
	}
	switch {
	case fset.NArg() > 0:
		return 2, fmt.Errorf("types takes no arguments (%s): name the model with -spec and -main, or -binding", strings.Join(fset.Args(), " "))
	case *bpath != "" && (*spec != "" || *main != ""):
		return 2, fmt.Errorf("types: -binding names the spec and its module: give -binding alone, or -spec and -main")
	case *bpath == "" && *spec == "":
		return 2, needs("types", "-spec model.qnt -main M, or -binding b.yaml")
	case *check && *o == "":
		return 2, fmt.Errorf("types -check compares a file: name it with -o (the file gotogether types -o wrote)")
	}
	if *pkg == "" {
		*pkg = "model"
		if *o != "" {
			if p := packageIn(filepath.Dir(*o)); p != "app" {
				*pkg = p
			}
		}
	}
	if err := typegen.CheckPackage(*pkg); err != nil {
		return 2, err
	}
	from := *spec
	path := *spec
	if *bpath != "" {
		b, err := binding.Load(*bpath)
		if err != nil {
			return 2, err
		}
		if err := b.CheckSpec(); err != nil {
			return 2, err
		}
		path, *main = b.SpecPath(), b.Module
		from = filepath.Join(filepath.Dir(*bpath), b.Spec)
	} else if literate.IsDoc(*spec) {
		if _, err := os.Stat(*spec); err == nil {
			if _, gen, err := literate.Write(*spec); err != nil {
				return 2, fmt.Errorf("types: %s: %w", *spec, err)
			} else {
				path = gen
			}
		}
	}
	if *main == "" {
		*main = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	// the header names the model from the file's directory, so that the
	// file is the same whichever directory it was generated from
	if *o != "" {
		if abs, err := filepath.Abs(from); err == nil {
			if dir, err := filepath.Abs(filepath.Dir(*o)); err == nil {
				if rel, err := filepath.Rel(dir, abs); err == nil {
					from = rel
				}
			}
		}
	}
	var have []byte
	if *check {
		var err error
		have, err = os.ReadFile(*o)
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(out, "FAIL %s: missing: run %s\n", *o, fixCommand(args))
			return 1, nil
		}
		if err != nil {
			return 2, err
		}
	}
	tc, err := typegen.Typecheck(ctx, *quint, path)
	if err != nil {
		return 2, err
	}
	m, err := typegen.Read(tc, *main)
	if err != nil {
		return 2, err
	}
	src, err := typegen.Generate(m, typegen.Options{Package: *pkg, From: from})
	if err != nil {
		return 2, err
	}
	summary := fmt.Sprintf("%s and %s of %s", plural(m.Types(), "type"), stateName(src), plural(m.Vars(), "variable"))
	if *check {
		if bytes.Equal(have, src) {
			fmt.Fprintf(out, "ok   %s: current for %s (module %s: %s)\n", *o, from, m.Module, summary)
			return 0, nil
		}
		fmt.Fprintf(out, "FAIL %s: stale: %s: run %s\n", *o, staleWhy(have, m), fixCommand(args))
		return 1, nil
	}
	if *o == "" {
		_, err := out.Write(src)
		return 0, err
	}
	if old, err := os.ReadFile(*o); err == nil && bytes.Equal(old, src) {
		fmt.Fprintf(out, "%s: current, unchanged (module %s: %s)\n", *o, m.Module, summary)
		return 0, nil
	}
	if err := os.WriteFile(*o, src, 0o644); err != nil {
		return 2, err
	}
	fmt.Fprintf(out, "%s: written from %s (module %s: %s)\n", *o, from, m.Module, summary)
	return 0, nil
}

// stateName is the name the generated file gave the state's struct
// (State, unless the model declares a type State).
func stateName(src []byte) string {
	for l := range strings.SplitSeq(string(src), "\n") {
		if rest, ok := strings.CutPrefix(l, "type State"); ok && strings.HasSuffix(rest, " struct {") {
			return "State" + strings.TrimSuffix(rest, " struct {")
		}
	}
	return "State"
}

// staleWhy says what differs between a file and the model now: the types
// and variables whose digests changed, else that the file was edited or
// written by another version.
func staleWhy(have []byte, m *typegen.Model) string {
	entries, ok := typegen.FileEntries(have)
	if !ok {
		return "not generated by gotogether types (it has no header of its)"
	}
	if c := typegen.Changes(m.Digests(), entries); len(c) > 0 {
		return strings.Join(c, ", ")
	}
	return "the model's types are as they were, but the file is not what this gotogether generates from them (edited by hand, or generated by another version)"
}

// fixCommand is the command that writes the file again: args without
// -check.
func fixCommand(args []string) string {
	var keep []string
	for _, a := range args {
		switch strings.TrimLeft(a, "-") {
		case "check", "check=true":
			continue
		}
		if strings.ContainsAny(a, " \t'\"") {
			a = "'" + a + "'"
		}
		keep = append(keep, a)
	}
	return "gotogether types " + strings.Join(keep, " ")
}
