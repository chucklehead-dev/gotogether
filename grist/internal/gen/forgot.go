package gen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// forgotten are the warnings of what an app forgot, which nothing else
// says (ROADMAP I26): an event its blocks append that nothing reads (no
// read model folds it, no processor handles it or delivers it, the
// model's own files read it nowhere: its events change no decision); the
// rules' generated mutants run by no test; commands no acceptance
// scenario takes, in an app that writes scenarios. own and rules are the
// app's model files; dir holds its Go files, read for the tests' wiring
// (best effort: a text search, so each warning says what it looked for).
func forgotten(c Catalog, dir string, own, rules []byte) (ws, errs []string) {
	ws, errs = unreadEvents(c, string(own)+"\n"+string(rules))
	goSrc, testSrc := goSources(dir)
	ws = append(ws, unwiredRuleMutants(c, testSrc)...)
	ws = append(ws, unscenarioed(c, goSrc, testSrc)...)
	return ws, errs
}

// unreadEvents warns of each event a block appends (or that no block
// declares appending) that no read model folds, no processor triggers
// on or delivers, and model (the app's model files) reads nowhere: by
// its type's name, a field's accessor or a read of it. One that no block
// can append either (the app has commands, every one declares its
// Appends, and none, nor any processor, names this event) is dead,
// surely a mistake: an error.
func unreadEvents(c Catalog, model string) (ws, errs []string) {
	declared := true
	for _, b := range c.Commands {
		declared = declared && b.Appends != nil
	}
	for _, e := range c.Events {
		if strings.HasPrefix(e.Name, "grist.") {
			continue
		}
		read := false
		for _, rm := range c.ReadModels {
			read = read || slices.Contains(rm.Types, e.Name)
		}
		for _, p := range c.Processors {
			read = read || p.On == e.Name || p.Success == e.Name
		}
		if read || modelReads(e, model) {
			continue
		}
		var by []string
		for _, b := range c.Commands {
			if slices.Contains(b.Appends, e.Name) {
				by = append(by, "command "+b.Name)
			}
		}
		for _, p := range c.Processors {
			if slices.Contains(p.Emits, e.Name) {
				by = append(by, "processor "+p.Name)
			}
		}
		appended := "is appended by " + strings.Join(by, ", ")
		if len(by) == 0 && len(c.Commands) == 0 {
			continue // an app of no command appends nothing: a test's catalog
		}
		if len(by) == 0 && declared {
			errs = append(errs, fmt.Sprintf("event %s is appended by no block (every command declares its grist.Appends, and none, nor any processor's Emits or Delivers, names it) and read by nothing (no read model folds it, no processor handles it, the model reads it nowhere): "+
				"it can never be written or matter. Declare the block that appends it, or remove it (grist.lock keeps its stored versions)", e.Name))
			continue
		}
		if len(by) == 0 {
			appended = "is appended by no block that declares it (grist.Appends, Emits, Delivers)"
		}
		ws = append(ws, fmt.Sprintf("event %s %s and read by nothing: no read model folds it, no processor handles it, and the model's rules and invariants read it nowhere, so its events change no decision%s. "+
			"If one should (a removal lowering stock), fold it: a grist.On(%s, ...) in that read model and its fold in model/%s_rules.qnt; if it is a record no decision reads, this warning says only that",
			e.Name, appended, foldHint(c, e), e.Name, c.Name))
	}
	return ws, errs
}

// modelReads reports whether model reads event e: its type's name as a
// string, or one of its generated accessors or reads (<event><Field>,
// <event>Read, <event>By<Tag>), the constructor aside, which builds one.
func modelReads(e CatalogEvent, model string) bool {
	if strings.Contains(model, fmt.Sprintf("%q", e.Name)) {
		return true
	}
	eq, err := QuintName(e.Name)
	if err != nil {
		return false
	}
	names := []string{eq + "Read"}
	fs, _ := scalarFields(e.Body)
	for _, f := range fs {
		names = append(names, eq+upper(f.json))
		if f.tag {
			names = append(names, eq+"By"+upper(f.json))
		}
	}
	for _, n := range names {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(n) + `\b`).MatchString(model) {
			return true
		}
	}
	return false
}

// foldHint names the read models that fold events sharing a tag key with
// e (its own key among them first): the likeliest to have to fold it.
func foldHint(c Catalog, e CatalogEvent) string {
	tagsOf := map[string][]string{}
	for _, o := range c.Events {
		tagsOf[o.Name] = o.Tags
	}
	var hints []string
	for _, rm := range c.ReadModels {
		var shared []string
		for _, k := range e.Tags {
			for _, t := range rm.Types {
				if slices.Contains(tagsOf[t], k) && !slices.Contains(shared, k) {
					shared = append(shared, k)
				}
			}
		}
		if len(shared) > 0 {
			sort.Strings(shared)
			hints = append(hints, fmt.Sprintf("%s folds events tagged %s too", rm.Name, strings.Join(shared, ", ")))
		}
	}
	if len(hints) == 0 {
		return ""
	}
	return " (" + strings.Join(hints, "; ") + ": should it fold this one?)"
}

// goSources are the package's Go files under dir, the tests apart: their
// text, concatenated.
func goSources(dir string) (src, tests string) {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	var s, t strings.Builder
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.HasSuffix(p, "_test.go") {
			t.Write(b)
			t.WriteByte('\n')
		} else {
			s.Write(b)
			s.WriteByte('\n')
		}
	}
	return s.String(), t.String()
}

// unwiredRuleMutants warns when the app has rules that guard commands
// (each with a generated mutant, gristtest.RuleMutants) and its tests,
// which exist, run none of them.
func unwiredRuleMutants(c Catalog, tests string) []string {
	var guarded []string
	for _, r := range c.Rules {
		if len(r.Guards) > 0 {
			guarded = append(guarded, r.Name)
		}
	}
	if len(guarded) == 0 || tests == "" {
		return nil
	}
	var missing []string
	if !strings.Contains(tests, "RuleMutants(") {
		missing = append(missing, "qtest.Mutants{Generated: gristtest.RuleMutants(App)} in TestMutants")
	}
	if !strings.Contains(tests, "DroppedRule(") {
		missing = append(missing, "gristtest.DroppedRule(App) in TestMutant, exploring where the rule refuses and expecting its claim")
	}
	if len(missing) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("rule %s has a generated mutant (the rule dropped: drop-<rule>) that no test runs, so nothing shows a run reaching its refusal; the package's tests have no %s",
		strings.Join(guarded, ", "), strings.Join(missing, ", nor "))}
}

// commandVar matches a command's registration, `X = grist.NewCommand(App,
// "name"` (or :=), for the variable naming it.
var commandVar = regexp.MustCompile(`(\w+)\s*:?=\s*grist\.NewCommand\(\s*\w+\s*,\s*"([^"]+)"`)

// unscenarioed warns, in an app whose tests write acceptance scenarios
// (gristtest.Scenario), of each command no scenario invokes
// (<command var>.With(...) inside a Scenario's arguments).
func unscenarioed(c Catalog, src, tests string) []string {
	if !strings.Contains(tests, "gristtest.Scenario(") {
		return nil
	}
	vars := map[string]string{} // command name -> its variable
	for _, m := range commandVar.FindAllStringSubmatch(src, -1) {
		vars[m[2]] = m[1]
	}
	scenarios := scenarioArgs(tests)
	var none []string
	for _, b := range c.Commands {
		v, ok := vars[b.Name]
		if !ok {
			continue // registered otherwise: not found by the search
		}
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(v) + `\.With\(`).MatchString(scenarios) {
			none = append(none, fmt.Sprintf("%s (%s)", b.Name, v))
		}
	}
	if len(none) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("command %s is taken by no acceptance scenario (gristtest.Scenario: no %s.With(...) among its steps), so no test states what a user does with it: "+
		"add a scenario that invokes it, claimed qtest.Possible by the model and exercised by a run", strings.Join(none, ", "), firstVar(none))}
}

// firstVar is the variable of the first of none ("name (Var)").
func firstVar(none []string) string {
	s := none[0]
	return strings.TrimSuffix(s[strings.LastIndex(s, "(")+1:], ")")
}

// scenarioArgs are the text of every gristtest.Scenario call's arguments
// in tests (to its closing parenthesis, strings and comments not
// skipped: a search, not a parse).
func scenarioArgs(tests string) string {
	var b strings.Builder
	for rest := tests; ; {
		i := strings.Index(rest, "gristtest.Scenario(")
		if i < 0 {
			break
		}
		rest = rest[i+len("gristtest.Scenario("):]
		depth, j := 1, 0
		for ; j < len(rest) && depth > 0; j++ {
			switch rest[j] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		b.WriteString(rest[:j])
		b.WriteByte('\n')
		rest = rest[j:]
	}
	return b.String()
}

// lockChanges says how next, grist.lock as the catalog makes it, differs
// from prev, the one checked in: versions and read models added or
// removed, a read model's fingerprint changed. nil when prev is missing
// or they agree.
func lockChanges(prev, next []byte) []string {
	if prev == nil {
		return []string{"there is no grist.lock: the first `<app> model` writes it; commit it beside the app (it is the record of what has been stored)"}
	}
	var a, b lockFile
	if json.Unmarshal(prev, &a) != nil || json.Unmarshal(next, &b) != nil {
		return nil
	}
	versions := func(l lockFile) map[string]string {
		out := map[string]string{}
		for _, e := range l.Events {
			for _, v := range e.Versions {
				out[fmt.Sprintf("%s version %d", e.Name, v.Version)] = v.Shape
			}
		}
		return out
	}
	fps := func(l lockFile) map[string]string {
		out := map[string]string{}
		for _, rm := range l.ReadModels {
			out[rm.Name] = fmt.Sprintf("%d %s", rm.Version, rm.Fingerprint)
		}
		return out
	}
	var out []string
	av, bv := versions(a), versions(b)
	for _, k := range sortedKeys(bv) {
		switch was, ok := av[k]; {
		case !ok:
			out = append(out, k+" is new")
		case was != bv[k]:
			out = append(out, k+" gained a field")
		}
	}
	af, bf := fps(a), fps(b)
	for _, k := range sortedKeys(bf) {
		switch was, ok := af[k]; {
		case !ok:
			out = append(out, "read model "+k+" is new")
		case was != bf[k]:
			out = append(out, "read model "+k+"'s fingerprint changed (its snapshots refold)")
		}
	}
	if len(out) == 0 && string(prev) != string(next) {
		out = append(out, "its text is not what the generator writes")
	}
	return out
}
