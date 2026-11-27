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
// drivers that run it (connect.Bind entries, connect.Switch keys), and
// the table of what the model says it leaves out (its `// @assume` lines,
// ROADMAP CW3), the code that exercises each (//quint:exercises) and the
// checks that monitor it (//quint:monitors). With -claims it lints
// design documents' marked claims (ROADMAP CW3's doc lint): a `Claim:`
// paragraph or list item must cite a property, (prop: name), or an
// assumption, (assume: name), the model has; -require cited fails a
// cited property nothing enforces or checks, and a cited assumption
// nothing targets. It prints the tables, writes each
// into a design document between its markers, where the document has
// them (-write), or compares the document's with them (-check, writing
// nothing: exit 1 when stale, naming the rows that changed). An
// annotation naming no property, action or assumption of the model, or
// annotating nothing, and an @assume line that declares none, fail (exit
// 1) under every mode; a property nothing enforces or no test checks, an
// action nothing records or drives, and an assumption nothing targets,
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
	exclude := fset.String("exclude", "", "leave these out of the tables (comma-separated): boolean vals out of the properties (scenario targets, mutant witnesses), actions out of the actions rows")
	name := fset.String("name", "", "the name of this model's tables in the document, <!-- gotogether:invariants NAME --> (default: the main module, or the binding's module); a document without named markers takes its unnamed ones")
	links := fset.String("links", "line", "line: each site a file:line link; name: a site by its function (by package and function when the tables span packages), linked to its file by its base name without a line, so moved code leaves the table current")
	lit := fset.Bool("literate", true, "treat a .md spec as a literate model, and a document that is one as its model's source (its .qnt written again); false: a .md spec is refused (name the .qnt your own tangler writes) and the document is plain Markdown")
	write := fset.String("write", "", "write the tables into this Markdown document: the properties' between its "+invtable.Begin+" and "+invtable.End+" lines, the actions' between "+invtable.ActionsBegin+" and "+invtable.ActionsEnd+", the assumptions' between "+invtable.AssumptionsBegin+" and "+invtable.AssumptionsEnd+", each where the document has its markers; named ones (<!-- gotogether:invariants NAME -->) hold one model's of several, and only -name's are written")
	check := fset.String("check", "", "compare the tables with this Markdown document's, writing nothing: exit 1 when they differ, naming the rows (for CI)")
	claims := fset.String("claims", "", "lint these Markdown design documents' claims (comma-separated), instead of printing the tables (with -write or -check, as well as): a paragraph, list item or quoted line beginning Claim: (or **Claim:**) must cite a property of the model, (prop: name, ...), or an assumption it lists, (assume: name), and each it cites must be the model's; fenced code is no prose")
	require := fset.String("require", "", "fail (exit 1), instead of warning, when a property is not enforced (a //quint:enforces) or checked (a //quint:checks in a test), an action not recorded (a //quint:action or a literal qobs.Record) or driven (a connect.Bind entry or connect.Switch key), a listed assumption (// @assume in the model) not exercised (a //quint:exercises), monitored (a //quint:monitors) or targeted (either), or a property a -claims claim cites not enforced and checked, an assumption it cites not targeted (cited): a comma-separated list of enforced, checked, recorded, driven, exercised, monitored, targeted, cited")
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
		case "enforced", "checked", "recorded", "driven", "exercised", "monitored", "targeted", "cited":
			required = append(required, r)
		default:
			return 2, fmt.Errorf("invariants: -require %s: give a comma-separated list of enforced and checked (each property), recorded and driven (each action), exercised, monitored and targeted (each assumption), cited (each property or assumption a -claims claim cites)", *require)
		}
	}
	claimDocs := list(*claims)
	if slices.Contains(required, "cited") && len(claimDocs) == 0 {
		return 2, fmt.Errorf("invariants: -require cited is of the claims a design document marks: name the documents with -claims doc.md")
	}
	claimSrc := map[string][]byte{}
	for _, d := range claimDocs {
		src, err := os.ReadFile(d)
		if err != nil {
			return 2, fmt.Errorf("invariants: -claims %s: %v (name the Markdown documents whose claims to lint)", d, err)
		}
		claimSrc[d] = src
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
	// claims alone lint the documents and print no table: only cited may
	// be required, the tables' kinds need a table in play
	claimsOnly := len(claimDocs) > 0 && doc == ""
	if claimsOnly {
		for _, r := range required {
			if r != "cited" {
				return 2, fmt.Errorf("invariants: -require %s is of the tables, and -claims alone prints none: require only cited of the claims, or give -check doc.md with the document's tables", r)
			}
		}
	}
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
		{name: "assumptions", kind: "assumptions", text: (*invtable.Table).MarkdownAssumptions, kinds: []string{"targeted"}},
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
			sb, se := literate.Markers("assumptions", *name)
			msg := fmt.Sprintf("%s has no table of model %s: add %s ... %s, %s ... %s, or %s ... %s, each alone on its line where the table goes (unnamed markers serve a document of one model)", doc, *name, ib, ie, ab, ae, sb, se)
			if others := slices.Concat(literate.MarkerNames(string(src), "invariants"), literate.MarkerNames(string(src), "actions"), literate.MarkerNames(string(src), "assumptions")); len(others) > 0 {
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
	// the actions table is in play when printed, or when the document
	// has its markers: otherwise no code implementing actions is read,
	// and none can fail the run (another model's drivers, unqualifiable)
	if claimsOnly {
		tables = nil
	}
	actionsInPlay := slices.ContainsFunc(tables, func(t docTable) bool { return t.name == "actions" })
	if !actionsInPlay && !claimsOnly {
		for _, r := range required {
			if r == "recorded" || r == "driven" {
				ab, ae := literate.Markers("actions", *name)
				return 2, fmt.Errorf("invariants: -require %s is of the actions table, and %s has none of model %s (%s ... %s, or unnamed): add the pair, or require only enforced and checked", r, doc, *name, ab, ae)
			}
		}
	}
	// the assumptions table likewise: a document's markers put it in
	// play; printed, it is there when the model lists any (below)
	if doc != "" && !slices.ContainsFunc(tables, func(t docTable) bool { return t.name == "assumptions" }) {
		for _, r := range required {
			if r == "exercised" || r == "monitored" || r == "targeted" {
				sb, se := literate.Markers("assumptions", *name)
				return 2, fmt.Errorf("invariants: -require %s is of the assumptions table, and %s has none of model %s (%s ... %s, or unnamed): add the pair, or require it of no assumption", r, doc, *name, sb, se)
			}
		}
	}
	patterns := fset.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	anns, problems, err := invtable.ScanFor(patterns, actionsInPlay)
	if err != nil {
		return 2, fmt.Errorf("invariants: %w", err)
	}
	anns, skippedDrivers := invtable.OtherSpecs(anns, path)
	tc, err := typegen.Typecheck(ctx, *quint, path)
	if err != nil {
		return 2, err
	}
	src, _ := os.ReadFile(path)
	m, err := invtable.Read(tc, *main, invtable.Comments(src))
	if err != nil {
		return 2, err
	}
	// the model's listed assumptions, before a selection: a val that
	// states one leaves the default properties, which -props may name
	if bad := m.Assume(src, path); len(bad) > 0 {
		for _, e := range bad {
			fmt.Fprintln(errw, literate.Rewrite(e, "."))
		}
		fmt.Fprintf(out, "FAIL %s of model %s declaring no assumption the table can show: no table %s (fix each as it says)\n", plural(len(bad), "@assume line"), m.Main, map[bool]string{true: "printed", false: "written"}[doc == ""])
		return 1, nil
	}
	if doc == "" && len(m.Assumptions) == 0 {
		tables = slices.DeleteFunc(tables, func(t docTable) bool { return t.name == "assumptions" })
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
		fmt.Fprintf(out, "FAIL %s naming no property, action or assumption of model %s, or annotating nothing: %s (fix each as it says)\n", plural(len(errs), "annotation"), m.Main, what)
		return 1, nil
	}
	if len(table.Others) > 0 {
		var mods []string
		n := 0
		for _, mod := range slices.Sorted(maps.Keys(table.Others)) {
			mods = append(mods, fmt.Sprintf("%s (%d)", mod, table.Others[mod]))
			n += table.Others[mod]
		}
		fmt.Fprintf(errw, "note: %s of other models (qualified by modules this spec does not have, or actions recorded in files annotated only for them), left out of the tables of model %s: %s\n", plural(n, "annotation name"), m.Main, strings.Join(mods, ", "))
	}
	if len(skippedDrivers) > 0 {
		var specs []string
		n := 0
		for _, sp := range slices.Sorted(maps.Keys(skippedDrivers)) {
			specs = append(specs, fmt.Sprintf("%s (%d)", sp, skippedDrivers[sp]))
			n += skippedDrivers[sp]
		}
		fmt.Fprintf(errw, "note: %s in files whose connect.Config names another spec, left out of the actions of model %s: %s\n", map[bool]string{true: "1 connect driver entry", false: fmt.Sprintf("%d connect driver entries", n)}[n == 1], m.Main, strings.Join(specs, ", "))
	}
	// each row a kind leaves bare: a FAIL of its own when required, else
	// one note a kind
	code := 0
	for _, t := range tables {
		for _, k := range t.kinds {
			if slices.Contains(required, k) || (k == "targeted" && (slices.Contains(required, "exercised") || slices.Contains(required, "monitored"))) {
				continue
			}
			if missing := table.Missing(k); len(missing) > 0 {
				fmt.Fprintln(errw, "note: "+missingNote(k, missing))
			}
		}
	}
	// the claims: an error each that cites nothing or what the model
	// lacks; each citation nothing backs a FAIL under -require cited,
	// else a note
	for _, d := range claimDocs {
		cc := table.CheckClaims(invtable.Claims(claimSrc[d], d))
		for _, e := range cc.Errors {
			fmt.Fprintln(errw, e)
		}
		for _, u := range cc.Unbacked {
			if slices.Contains(required, "cited") {
				fmt.Fprintln(errw, "FAIL "+u)
			} else {
				fmt.Fprintln(errw, "note: "+u+" (cited is not required)")
			}
		}
		switch {
		case len(cc.Errors) > 0:
			fmt.Fprintf(out, "FAIL %s: %s in its claims, each citing nothing or what model %s lacks (fix each as it says)\n", d, plural(len(cc.Errors), "problem"), m.Main)
			code = 1
		case len(cc.Unbacked) > 0 && slices.Contains(required, "cited"):
			fmt.Fprintf(out, "FAIL %s: %s the code does not back (-require cited; fix each as it says)\n", d, plural(len(cc.Unbacked), "citation"))
		case cc.Claims == 0:
			fmt.Fprintf(out, "ok   %s: no claims marked (begin one with Claim: and cite (prop: name))\n", d)
		default:
			fmt.Fprintf(out, "ok   %s: %s, citing %s and %s of model %s\n", d, plural(cc.Claims, "claim"), properties(cc.Props), plural(cc.Assumes, "assumption"), m.Main)
		}
		if len(cc.Unbacked) > 0 && slices.Contains(required, "cited") {
			code = 1
		}
	}
	if claimsOnly {
		return code, nil
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
		of := ""
		if len(parts) == 0 {
			of = " of model " + m.Main
		}
		switch t.name {
		case "properties":
			parts = append(parts, fmt.Sprintf("%s%s: %d enforced, %d checked", properties(len(table.Rows)), of, count(table, "enforced"), count(table, "checked")))
		case "actions":
			parts = append(parts, fmt.Sprintf("%s%s: %d recorded, %d driven", plural(len(table.Actions), "action"), of, count(table, "recorded"), count(table, "driven")))
		case "assumptions":
			parts = append(parts, fmt.Sprintf("%s%s: %d exercised, %d monitored", plural(len(table.Assumptions), "assumption"), of, count(table, "exercised"), count(table, "monitored")))
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
	if len(table.Assumptions) > 0 {
		fmt.Fprint(out, "\n"+table.MarkdownAssumptions(""))
	}
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

// missingNote is the one line that names the rows of a kind not
// required that have nothing of it: "5 actions nothing records
// (recorded is not required): a, b, ...".
func missingNote(kind string, names []string) string {
	what := map[string]string{"enforced": "nothing enforces", "checked": "no test checks", "recorded": "nothing records", "driven": "nothing drives", "targeted": "nothing targets"}[kind]
	rows := properties(len(names))
	switch kind {
	case "recorded", "driven":
		rows = plural(len(names), "action")
	case "targeted":
		rows = plural(len(names), "assumption")
	}
	shown := names
	if len(shown) > 10 {
		shown = append(slices.Clone(shown[:10]), "...")
	}
	return fmt.Sprintf("%s %s (%s is not required): %s", rows, what, kind, strings.Join(shown, ", "))
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
	for _, r := range t.Assumptions {
		if (kind == "exercised" && len(r.Exercised) > 0) || (kind == "monitored" && len(r.Monitored) > 0) {
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
