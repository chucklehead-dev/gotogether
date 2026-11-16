package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/invtable"
	"github.com/chucklehead-dev/gotogether/internal/literate"
	"github.com/chucklehead-dev/gotogether/internal/typegen"
)

// invariants is the invariants command: the table of a model's
// properties, where the Go code enforces each and which tests check it,
// from //quint:enforces and //quint:checks directives (ROADMAP CW5's
// static half, internal/invtable). It prints the table, writes it into a
// design document between its markers (-write), or compares the
// document's with it (-check, writing nothing: exit 1 when stale, naming
// the rows that changed). A directive naming no property of the model, or
// annotating nothing, fails (exit 1) under every mode; a property nothing
// enforces or no test checks is a warning, a failure under -require. Exit
// 2 is a bad invocation, a Go file that does not parse, or a model quint
// refuses.
func invariants(ctx context.Context, out, errw io.Writer, args []string) (int, error) {
	fset := commandFlagsWithHandling("invariants", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	spec := fset.String("spec", "", "the model: a .qnt, or a literate model's .md (written to its .qnt, as tangle does)")
	main := fset.String("main", "", "the model's main module (default: the spec's file name, as quint's)")
	bpath := fset.String("binding", "", "take -spec and -main from this binding, and only its invariants and eventually vals as the properties")
	write := fset.String("write", "", "write the table into this Markdown document, between its "+invtable.Begin+" and "+invtable.End+" lines")
	check := fset.String("check", "", "compare the table with this Markdown document's, writing nothing: exit 1 when they differ, naming the rows (for CI)")
	require := fset.String("require", "", "fail (exit 1) when a property is not: enforced, checked, or both (enforced,checked), instead of warning")
	quint := fset.String("quint", "quint", "quint binary")
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fset.SetOutput(out)
			fset.Usage()
			return 0, nil
		}
		return 2, fmt.Errorf("invariants: %v (gotogether invariants -h lists its flags)", err)
	}
	var required []string
	for r := range strings.SplitSeq(*require, ",") {
		switch r = strings.TrimSpace(r); r {
		case "":
		case "enforced", "checked":
			required = append(required, r)
		default:
			return 2, fmt.Errorf("invariants: -require %s: give enforced, checked, or enforced,checked", *require)
		}
	}
	switch {
	case *bpath != "" && (*spec != "" || *main != ""):
		return 2, fmt.Errorf("invariants: -binding names the spec and its module: give -binding alone, or -spec and -main")
	case *bpath == "" && *spec == "":
		return 2, needs("invariants", "-spec model.qnt -main M, or -binding b.yaml")
	case *write != "" && *check != "":
		return 2, fmt.Errorf("invariants: -write writes the table and -check compares it, writing nothing: give one")
	}
	doc := cmp.Or(*write, *check)
	if doc != "" {
		if _, err := os.Stat(doc); err != nil {
			return 2, fmt.Errorf("invariants: the document %s: %v (name the Markdown document the table goes in)", doc, err)
		}
	}
	patterns := fset.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	anns, problems, err := invtable.Scan(patterns)
	if err != nil {
		return 2, fmt.Errorf("invariants: %w", err)
	}
	path, names := *spec, []string(nil)
	if *bpath != "" {
		b, err := binding.Load(*bpath)
		if err != nil {
			return 2, err
		}
		if err := b.CheckSpec(); err != nil {
			return 2, err
		}
		path, *main = b.SpecPath(), b.Module
		names = append(slices.Clone(b.Invariants), b.Eventually...)
	} else if literate.IsDoc(*spec) {
		if _, err := os.Stat(*spec); err == nil {
			if _, gen, err := literate.Write(*spec); err != nil {
				return 2, fmt.Errorf("invariants: %s: %w", *spec, err)
			} else {
				path = gen
			}
		}
	}
	if *main == "" {
		*main = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	tc, err := typegen.Typecheck(ctx, *quint, path)
	if err != nil {
		return 2, err
	}
	src, _ := os.ReadFile(path)
	m, err := invtable.Read(tc, *main, invtable.Comments(src))
	if err != nil {
		return 2, err
	}
	if names != nil {
		if err := m.Restrict(names); err != nil {
			return 2, err
		}
	}
	table, errs := m.Table(anns)
	if errs = append(problems, errs...); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(errw, e)
		}
		what := "the table is not written"
		if *write == "" && *check == "" {
			what = "no table printed"
		}
		fmt.Fprintf(out, "FAIL %s naming no property of model %s, or annotating nothing: %s (fix each as it says)\n", plural(len(errs), "directive"), m.Main, what)
		return 1, nil
	}
	code := 0
	for _, w := range table.Warnings() {
		fmt.Fprintln(errw, "warning: "+w)
	}
	if len(required) > 0 {
		if failed := table.Warnings(required...); len(failed) > 0 {
			for _, w := range failed {
				fmt.Fprintln(errw, "FAIL "+w)
			}
			code = 1
		}
	}
	summary := fmt.Sprintf("%s of model %s: %d enforced, %d checked", properties(len(table.Rows)), m.Main, count(table, true), count(table, false))
	switch {
	case *check != "":
		have, err := literate.Marked(*check, invtable.Begin, invtable.End)
		if err != nil {
			fmt.Fprintf(out, "FAIL %s: missing: %v; then run %s\n", *check, err, invariantsFix(args))
			return 1, nil
		}
		want := table.Markdown(filepath.Dir(*check))
		if have == want {
			fmt.Fprintf(out, "ok   %s: current (%s)\n", *check, summary)
			return code, nil
		}
		why := strings.Join(invtable.Changes(have, want), ", ")
		if why == "" {
			why = "the table was edited by hand"
		}
		fmt.Fprintf(out, "FAIL %s: stale: %s: run %s\n", *check, why, invariantsFix(args))
		return 1, nil
	case *write != "":
		changed, err := literate.WriteMarked(*write, invtable.Begin, invtable.End, table.Markdown(filepath.Dir(*write)))
		if err != nil {
			return 2, fmt.Errorf("invariants -write: %w", err)
		}
		if changed {
			fmt.Fprintf(out, "%s: written (%s)\n", *write, summary)
		} else {
			fmt.Fprintf(out, "%s: current, unchanged (%s)\n", *write, summary)
		}
		return code, nil
	}
	fmt.Fprint(out, table.Markdown(""))
	return code, nil
}

// properties is "1 property" or "n properties".
func properties(n int) string {
	if n == 1 {
		return "1 property"
	}
	return fmt.Sprintf("%d properties", n)
}

// count is how many of the table's properties are enforced (or, with
// enforced false, checked).
func count(t *invtable.Table, enforced bool) int {
	n := 0
	for _, r := range t.Rows {
		if (enforced && len(r.Enforced) > 0) || (!enforced && len(r.Checked) > 0) {
			n++
		}
	}
	return n
}

// invariantsFix is the command that writes the document's table again:
// args with -check's document given to -write.
func invariantsFix(args []string) string {
	var keep []string
	for _, a := range args {
		switch {
		case a == "-check" || a == "--check":
			a = "-write"
		case strings.HasPrefix(a, "-check=") || strings.HasPrefix(a, "--check="):
			a = "-write=" + a[strings.Index(a, "=")+1:]
		}
		if strings.ContainsAny(a, " \t'\"") {
			a = "'" + a + "'"
		}
		keep = append(keep, a)
	}
	return "gotogether invariants " + strings.Join(keep, " ")
}
