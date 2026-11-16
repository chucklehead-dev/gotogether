package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/invtable"
	"github.com/chucklehead-dev/gotogether/internal/literate"
	"github.com/chucklehead-dev/gotogether/internal/typegen"
	"gopkg.in/yaml.v3"
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
	spec := fset.String("spec", "", "the model: a .qnt, or a literate model's .md (written to its .qnt, as tangle does; -literate=false refuses a .md)")
	main := fset.String("main", "", "the model's main module (default: the spec's file name, as quint's)")
	bpath := fset.String("binding", "", "take -spec and -main from this binding, and only its invariants and eventually vals as the properties (unless -props or -invariant)")
	props := fset.String("props", "", "the properties, exactly these boolean vals in this order (comma-separated), instead of every boolean val or the binding's")
	invariant := fset.String("invariant", "", "the properties: the conjuncts of these vals (comma-separated; val safe = and { a, b } gives a and b), expanded through conjuncts that are themselves conjunctions of vals; a val that is no conjunction is itself")
	exclude := fset.String("exclude", "", "leave these boolean vals out of the properties (comma-separated): scenario targets, mutant witnesses")
	name := fset.String("name", "", "the name of this model's tables in the document, <!-- gotogether:invariants NAME --> (default: the main module, or the binding's module); a document without named markers takes its unnamed ones")
	links := fset.String("links", "line", "line: each site a file:line link; name: a site by package and function, linked to its file without a line, so moved code leaves the table current")
	lit := fset.Bool("literate", true, "treat a .md spec as a literate model, and a document that is one as its model's source (its .qnt written again); false: a .md spec is refused (name the .qnt your own tangler writes) and the document is plain Markdown")
	write := fset.String("write", "", "write the tables into this Markdown document: the properties' between its "+invtable.Begin+" and "+invtable.End+" lines, the actions' between "+invtable.ActionsBegin+" and "+invtable.ActionsEnd+", each where the document has its markers; named ones (<!-- gotogether:invariants NAME -->) hold one model's of several, and only -name's are written")
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
	if *links != "line" && *links != "name" {
		return 2, fmt.Errorf("invariants: -links %s: give line (file:line links) or name (package and function, linked to the file)", *links)
	}
	switch {
	case *props != "" && *invariant != "":
		return 2, fmt.Errorf("invariants: -props names the properties and -invariant derives them: give one (and -exclude to leave some out)")
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
	if !*lit && literate.IsDoc(*spec) {
		return 2, fmt.Errorf("invariants: -spec %s is Markdown and -literate=false reads no model from it: name the .qnt your tangler writes from it (%s)", *spec, literate.GeneratedPath(*spec))
	}
	path, names := *spec, []string(nil)
	var b *binding.Binding
	if *bpath != "" {
		var err error
		if b, err = binding.Load(*bpath); err != nil {
			return 2, err
		}
		if err := b.CheckSpec(); err != nil {
			return 2, err
		}
		path, *main = b.SpecPath(), b.Module
		names = append(slices.Clone(b.Invariants), b.Eventually...)
		if *name == "" {
			*name = bindingModule(*bpath, b.Module)
		}
	} else if literate.IsDoc(*spec) {
		if _, err := os.Stat(*spec); err == nil {
			if _, gen, err := literate.Write(*spec); err != nil {
				return 2, fmt.Errorf("invariants: %s: %w (a model another tool tangles: -literate=false and its .qnt)", *spec, err)
			} else {
				path = gen
			}
		}
	}
	if *main == "" {
		*main = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if *name == "" {
		*name = *main
	}
	// the tables: both printed, else those whose markers the document
	// has, named for this model or, when it has none of that name,
	// unnamed
	tables := []docTable{
		{name: "properties", kind: "invariants", text: (*invtable.Table).Markdown, kinds: []string{"enforced", "checked"}},
		{name: "actions", kind: "actions", text: (*invtable.Table).MarkdownActions, kinds: []string{"recorded", "driven"}},
	}
	if doc != "" {
		src, _ := os.ReadFile(doc)
		var have []docTable
		for _, t := range tables {
			for _, n := range []string{*name, ""} {
				if t.begin, t.end = literate.Markers(t.kind, n); hasLine(string(src), t.begin) || hasLine(string(src), t.end) {
					have = append(have, t)
					break
				}
			}
		}
		tables = have
		if len(tables) == 0 {
			ib, ie := literate.Markers("invariants", *name)
			ab, ae := literate.Markers("actions", *name)
			msg := fmt.Sprintf("%s has no table of model %s: add %s ... %s, or %s ... %s, each alone on its line where the table goes (unnamed markers serve a document of one model)", doc, *name, ib, ie, ab, ae)
			if others := slices.Concat(literate.MarkerNames(string(src), "invariants"), literate.MarkerNames(string(src), "actions")); len(others) > 0 {
				slices.Sort(others)
				msg += fmt.Sprintf("; its tables are named %s (-name picks one)", strings.Join(slices.Compact(others), ", "))
			}
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
	tc, err := typegen.Typecheck(ctx, *quint, path)
	if err != nil {
		return 2, err
	}
	src, _ := os.ReadFile(path)
	m, err := invtable.Read(tc, *main, invtable.Comments(src))
	if err != nil {
		return 2, err
	}
	switch {
	case *props != "":
		err = m.Only("-props", list(*props))
	case *invariant != "":
		var ps []string
		if ps, err = m.Conjuncts(list(*invariant)); err == nil {
			err = m.Only("-invariant", ps)
		}
	case names != nil:
		err = m.Restrict(names)
	}
	if err == nil && *exclude != "" {
		err = m.Exclude(list(*exclude))
	}
	if err != nil {
		return 2, fmt.Errorf("invariants: %w", err)
	}
	table, errs := m.Table(anns)
	table.ByName = *links == "name"
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
	if len(table.Others) > 0 {
		var mods []string
		n := 0
		for _, mod := range slices.Sorted(maps.Keys(table.Others)) {
			mods = append(mods, fmt.Sprintf("%s (%d)", mod, table.Others[mod]))
			n += table.Others[mod]
		}
		fmt.Fprintf(errw, "note: %s qualified by modules this spec does not have (other models'), left out of the table of model %s: %s\n", plural(n, "annotation name"), m.Main, strings.Join(mods, ", "))
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
			writeMarked := literate.WriteMarked
			if !*lit {
				writeMarked = literate.WriteMarkedText
			}
			c, err := writeMarked(*write, t.begin, t.end, t.text(table, filepath.Dir(*write)))
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
	kind       string // the markers' kind: invariants, actions
	begin, end string
	text       func(t *invtable.Table, dir string) string
	kinds      []string
}

// list is a comma-separated flag's values.
func list(s string) []string {
	var out []string
	for v := range strings.SplitSeq(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// hasLine reports whether src has line as one of its lines (spaces
// around it aside): a marker, not text that mentions it.
func hasLine(src, line string) bool {
	for l := range strings.SplitSeq(src, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

// bindingModule is the module a binding file names, before an instance
// module is resolved to the module it imports (Load's Module): the name
// its tables take in a document. fallback when the file says none.
func bindingModule(path, fallback string) string {
	src, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	var head struct {
		Module string `yaml:"module"`
	}
	if yaml.Unmarshal(src, &head) != nil || head.Module == "" {
		return fallback
	}
	return head.Module
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
