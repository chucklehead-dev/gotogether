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
// static half, internal/invtable), and the table of its actions, the code
// that records each (//quint:action, literal qobs.Record) and the
// drivers that run it (connect.Bind entries, connect.Switch keys). It
// prints both, writes each into a design document between its markers,
// where the document has them (-write), or compares the document's with
// them (-check, writing nothing: exit 1 when stale, naming the rows that
// changed). An annotation naming no property or action of the model, or
// annotating nothing, fails (exit 1) under every mode; a property nothing
// enforces or no test checks, and an action nothing records or drives,
// is a warning (of the tables in play), a failure under -require. Exit
// 2 is a bad invocation, a Go file that does not parse, or a model quint
// refuses.
func invariants(ctx context.Context, out, errw io.Writer, args []string) (int, error) {
	fset := commandFlagsWithHandling("invariants", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	spec := fset.String("spec", "", "the model: a .qnt, or a literate model's .md (written to its .qnt, as tangle does)")
	main := fset.String("main", "", "the model's main module (default: the spec's file name, as quint's)")
	bpath := fset.String("binding", "", "take -spec and -main from this binding, and only its invariants and eventually vals as the properties")
	write := fset.String("write", "", "write the tables into this Markdown document: the properties' between its "+invtable.Begin+" and "+invtable.End+" lines, the actions' between "+invtable.ActionsBegin+" and "+invtable.ActionsEnd+", each where the document has its markers")
	check := fset.String("check", "", "compare the tables with this Markdown document's, writing nothing: exit 1 when they differ, naming the rows (for CI)")
	require := fset.String("require", "", "fail (exit 1), instead of warning, when a property is not enforced (a //quint:enforces) or checked (a //quint:checks in a test), or an action not recorded (a //quint:action or a literal qobs.Record) or driven (a connect.Bind entry or connect.Switch key): a comma-separated list of enforced, checked, recorded, driven")
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
		case "enforced", "checked", "recorded", "driven":
			required = append(required, r)
		default:
			return 2, fmt.Errorf("invariants: -require %s: give a comma-separated list of enforced and checked (each property), recorded and driven (each action)", *require)
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
	// the tables: both printed, else those whose markers the document has
	tables := []docTable{
		{"properties", invtable.Begin, invtable.End, (*invtable.Table).Markdown, []string{"enforced", "checked"}},
		{"actions", invtable.ActionsBegin, invtable.ActionsEnd, (*invtable.Table).MarkdownActions, []string{"recorded", "driven"}},
	}
	if doc != "" {
		src, _ := os.ReadFile(doc)
		tables = slices.DeleteFunc(tables, func(t docTable) bool {
			return !strings.Contains(string(src), t.begin) && !strings.Contains(string(src), t.end)
		})
		if len(tables) == 0 {
			msg := fmt.Sprintf("%s has neither %s ... %s nor %s ... %s: add the two lines where each table goes, each alone on its line", doc, invtable.Begin, invtable.End, invtable.ActionsBegin, invtable.ActionsEnd)
			if *check != "" {
				fmt.Fprintf(out, "FAIL %s: missing: %s; then run %s\n", doc, msg, invariantsFix(args))
				return 1, nil
			}
			return 2, fmt.Errorf("invariants -write: %s", msg)
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
		what := "the tables are not written"
		if *write == "" && *check == "" {
			what = "no table printed"
		}
		fmt.Fprintf(out, "FAIL %s naming no property or action of model %s, or annotating nothing: %s (fix each as it says)\n", plural(len(errs), "annotation"), m.Main, what)
		return 1, nil
	}
	var shown []string
	for _, t := range tables {
		shown = append(shown, t.kinds...)
	}
	code := 0
	for _, w := range table.Warnings(shown...) {
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
	var parts []string
	for _, t := range tables {
		if t.name == "properties" {
			parts = append(parts, fmt.Sprintf("%s of model %s: %d enforced, %d checked", properties(len(table.Rows)), m.Main, count(table, "enforced"), count(table, "checked")))
		} else {
			of := ""
			if len(parts) == 0 {
				of = " of model " + m.Main
			}
			parts = append(parts, fmt.Sprintf("%s%s: %d recorded, %d driven", plural(len(table.Actions), "action"), of, count(table, "recorded"), count(table, "driven")))
		}
	}
	summary := strings.Join(parts, "; ")
	switch {
	case *check != "":
		var why []string
		for _, t := range tables {
			have, err := literate.Marked(*check, t.begin, t.end)
			if err != nil {
				fmt.Fprintf(out, "FAIL %s: missing: %v; then run %s\n", *check, err, invariantsFix(args))
				return 1, nil
			}
			changes := invtable.Changes(have, t.text(table, filepath.Dir(*check)))
			if have != t.text(table, filepath.Dir(*check)) && len(changes) == 0 {
				changes = []string{"the " + t.name + " table was edited by hand"}
			}
			for _, c := range changes {
				if c == "the header" {
					c = "the " + t.name + " table's header"
				}
				why = append(why, c)
			}
		}
		if len(why) == 0 {
			fmt.Fprintf(out, "ok   %s: current (%s)\n", *check, summary)
			return code, nil
		}
		fmt.Fprintf(out, "FAIL %s: stale: %s: run %s\n", *check, strings.Join(why, ", "), invariantsFix(args))
		return 1, nil
	case *write != "":
		changed := false
		for _, t := range tables {
			c, err := literate.WriteMarked(*write, t.begin, t.end, t.text(table, filepath.Dir(*write)))
			if err != nil {
				return 2, fmt.Errorf("invariants -write: %w", err)
			}
			changed = changed || c
		}
		if changed {
			fmt.Fprintf(out, "%s: written (%s)\n", *write, summary)
		} else {
			fmt.Fprintf(out, "%s: current, unchanged (%s)\n", *write, summary)
		}
		return code, nil
	}
	fmt.Fprint(out, table.Markdown("")+"\n"+table.MarkdownActions(""))
	return code, nil
}

// A docTable is one of the tables a document may hold: its markers, its
// text (paths relative to a directory) and the kinds of warning it shows.
type docTable struct {
	name       string
	begin, end string
	text       func(t *invtable.Table, dir string) string
	kinds      []string
}

// properties is "1 property" or "n properties".
func properties(n int) string {
	if n == 1 {
		return "1 property"
	}
	return fmt.Sprintf("%d properties", n)
}

// count is how many of the table's rows are of kind: properties
// enforced or checked, actions recorded or driven.
func count(t *invtable.Table, kind string) int {
	n := 0
	for _, r := range t.Rows {
		if (kind == "enforced" && len(r.Enforced) > 0) || (kind == "checked" && len(r.Checked) > 0) {
			n++
		}
	}
	for _, r := range t.Actions {
		if (kind == "recorded" && len(r.Recorded) > 0) || (kind == "driven" && len(r.Driven) > 0) {
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
