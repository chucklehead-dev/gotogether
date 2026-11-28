package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// fixture is the invariants fixture's directory: a model, a counter
// annotated on a method's doc, a statement and two tests, a binding, and
// a design document whose table is checked in (the golden).
const fixture = "testdata/invariants"

// golem is the fixture of golem's shapes (#144): two models, attempts and
// publication, whose qualified annotations share a tree of two packages,
// and one document holding each model's table under its own name (the
// golden, written as docs/design.md says).
const golem = "testdata/invariants-golem"

// copyFixture copies the fixture into a new directory.
func copyFixture(t *testing.T) string { return copyTree(t, fixture) }

// copyTree copies the directory from into a new directory.
func copyTree(t *testing.T, from string) string {
	t.Helper()
	dir := t.TempDir()
	err := filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		to := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		return os.WriteFile(to, src, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// edit replaces old with new in the file at path, once.
func edit(t *testing.T, path, old, new string) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(src, []byte(old)) {
		t.Fatalf("%s has no %q", path, old)
	}
	if err := os.WriteFile(path, bytes.Replace(src, []byte(old), []byte(new), 1), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The checked-in document's tables are what the fixture generates now,
// from this directory as from the fixture's (paths are the document's):
// -check passes, warning of the property nothing enforces or checks and
// the action nothing records or drives, and -require makes the warnings
// of its kinds fail. The fixture records reset and drives it with qobs
// and connect imported under other names.
func TestInvariantsGolden(t *testing.T) {
	skip.Quint(t)
	doc := fixture + "/docs/walkthrough.md"
	var out, errw bytes.Buffer
	args := []string{"-spec", fixture + "/model/counter.qnt", "-check", doc, "-require", "enforced,recorded", fixture + "/counter"}
	code, err := invariants(t.Context(), &out, &errw, args)
	if err != nil || !strings.Contains(out.String(), "ok   "+doc+": current (3 properties of model counter: 2 enforced, 2 checked; 3 actions: 2 recorded, 2 driven; 3 assumptions: 1 exercised, 1 monitored)") {
		t.Fatalf("the checked-in table: %d, %v\n%s%s(write it again: cd %s && gotogether invariants -spec model/counter.qnt -write docs/walkthrough.md ./counter)", code, err, out.String(), errw.String(), fixture)
	}
	// a required kind's bare rows each a FAIL; another's one note
	want := "note: 1 property no test checks (checked is not required): atCap\n" +
		"note: 1 action nothing drives (driven is not required): idle\n" +
		"note: 1 assumption nothing targets (targeted is not required): capFixed\n" +
		"FAIL property atCap: nothing enforces it: put //quint:enforces counter.atCap on the code that does\n" + // the fixture qualifies its annotations: so does the fix
		"FAIL action idle: nothing records it: put //quint:action action:idle on the function that does it, or record it with qobs.Record(ctx, \"idle\", ...)\n"
	if code != 1 || errw.String() != want {
		t.Errorf("-require enforced,recorded: exit %d\n%s\nwant exit 1 and\n%s", code, errw.String(), want)
	}
}

// -write writes the tables and -check finds them current; a line moved
// in the code makes them stale, naming the rows of both and the command
// that writes them, and -check writes nothing; a directive naming no
// property, and a driver's entry naming no action, fail under -check as
// under -write, each at its file:line with the nearest name.
func TestInvariantsWriteCheck(t *testing.T) {
	skip.Quint(t)
	dir := copyFixture(t)
	doc := filepath.Join(dir, "docs", "walkthrough.md")
	edit(t, doc, "| `atCap` | | — | — |\n", "")
	edit(t, doc, "| `idle` | — | — |\n", "")
	spec, code := filepath.Join(dir, "model", "counter.qnt"), filepath.Join(dir, "counter")
	run := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errw bytes.Buffer
		n, err := invariants(t.Context(), &out, &errw, args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return n, out.String(), errw.String()
	}
	if n, out, _ := run("-spec", spec, "-check", doc, code); n != 1 || !strings.Contains(out, "FAIL "+doc+": stale: row atCap added, row idle added: run gotogether invariants -spec "+spec+" -write "+doc+" "+code+"\n") {
		t.Fatalf("a row missing: %d\n%s", n, out)
	}
	if n, out, _ := run("-spec", spec, "-write", doc, code); n != 0 || out != doc+": written (3 properties of model counter: 2 enforced, 2 checked; 3 actions: 2 recorded, 2 driven; 3 assumptions: 1 exercised, 1 monitored)\n" {
		t.Fatalf("write: %d\n%s", n, out)
	}
	if n, out, _ := run("-spec", spec, "-write", doc, code); n != 0 || !strings.Contains(out, "current, unchanged") {
		t.Fatalf("write again: %d\n%s", n, out)
	}
	written, _ := os.ReadFile(doc)
	golden, _ := os.ReadFile(fixture + "/docs/walkthrough.md")
	if !bytes.Equal(written, golden) {
		t.Errorf("written from another directory, the document differs from the golden:\n%s", written)
	}
	// Reset moves down a line: its rows are stale, the others are not
	edit(t, filepath.Join(code, "counter.go"), "// Reset starts again from zero,", "// Reset starts again from zero,\n// whatever the count,")
	if n, out, _ := run("-spec", spec, "-check", doc, code); n != 1 || !strings.Contains(out, "stale: row nonNegative changed, row reset changed, row oneCaller changed: run gotogether invariants") {
		t.Errorf("a moved line: %d\n%s", n, out)
	}
	if now, _ := os.ReadFile(doc); !bytes.Equal(now, written) {
		t.Errorf("-check wrote the document")
	}
	// a misspelled property and a misspelled action, under -check: an
	// error at each one's line
	edit(t, filepath.Join(code, "counter.go"), "//quint:enforces withinCap\n", "//quint:enforces withinCapp\n")
	edit(t, filepath.Join(code, "counter_test.go"), `Action("inc"`, `Action("ince"`)
	n, out, errw := run("-spec", spec, "-check", doc, code)
	at := filepath.Join(code, "counter.go") + ":23: //quint:enforces names withinCapp: model counter has no property withinCapp: did you mean withinCap? name one of model counter's properties: withinCap, nonNegative, atCap\n" +
		filepath.Join(code, "counter_test.go") + `:39: .Action("ince", ...) names ince, which is no action of model counter: did you mean inc? name one of its actions (inc, reset, idle), or leave code of another model out of the patterns` + "\n"
	if n != 1 || errw != at || out != "FAIL 2 annotations naming no property, action or assumption of model counter, or annotating nothing: the tables are not written (fix each as it says)\n" {
		t.Errorf("unknown property: %d\n%s%s\nwant\n%s", n, out, errw, at)
	}
}

// -binding takes the model from a binding, and its invariants as the
// properties: a val of the model it does not check is named as such. A
// document with the properties' markers alone gets that table alone,
// with no warning of actions.
// Qualified names resolve through the instance (counter::withinCap) and
// the module that declares the val (counterBase.nonNegative).
func TestInvariantsBinding(t *testing.T) {
	skip.Quint(t)
	dir := copyFixture(t)
	b := filepath.Join(dir, "counter.binding.yaml")
	doc := filepath.Join(dir, "props.md")
	if err := os.WriteFile(doc, []byte("# Properties\n\n<!-- gotogether:invariants -->\n<!-- /gotogether:invariants -->\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if n, err := invariants(t.Context(), &out, &errw, []string{"-binding", b, "-write", doc, "-require", "enforced,checked", filepath.Join(dir, "counter")}); n != 0 || err != nil || errw.Len() != 0 || out.String() != doc+": written (2 properties of model counterBase: 2 enforced, 2 checked)\n" {
		t.Fatalf("%d, %v\n%s%s", n, err, out.String(), errw.String())
	}
	if src, _ := os.ReadFile(doc); strings.Contains(string(src), "atCap") || strings.Contains(string(src), "| action |") || strings.Count(string(src), "\n") != 8 || !strings.Contains(string(src), "| `nonNegative` | n is never negative. | `Counter.Reset`") {
		t.Fatalf("the document:\n%s", src)
	}
	edit(t, filepath.Join(dir, "counter", "counter_test.go"), "//quint:checks withinCap\n", "//quint:checks withinCap atCap\n")
	out.Reset()
	if n, err := invariants(t.Context(), &out, &errw, []string{"-binding", b, filepath.Join(dir, "counter")}); n != 1 || err != nil || !strings.Contains(errw.String(), "counter_test.go:13: //quint:checks names atCap: atCap is a val of model counterBase that this table leaves out (the binding does not check it): add it to the selection, or name one the table has (withinCap, nonNegative)\n") {
		t.Errorf("a val the binding does not check: %d, %v\n%s%s", n, err, out.String(), errw.String())
	}
}

// A bad invocation exits 2 and says what to give, before quint runs.
func TestInvariantsFlags(t *testing.T) {
	doc := fixture + "/docs/walkthrough.md"
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "invariants needs -spec model.qnt -main M, or -binding b.yaml: gotogether invariants -spec"},
		{[]string{"-binding", "b.yaml", "-main", "m"}, "-binding names the spec and its module"},
		{[]string{"-spec", "m.qnt", "-write", doc, "-check", doc}, "-write writes the table and -check compares it, writing nothing: give one"},
		{[]string{"-spec", "m.qnt", "-require", "enforced,tested"}, "-require enforced,tested: give a comma-separated list of enforced and checked (each property), recorded and driven (each action), exercised, monitored and targeted (each assumption)"},
		{[]string{"-spec", "m.qnt", "-write", fixture + "/counter.binding.yaml"}, "invariants -write: " + fixture + "/counter.binding.yaml has no table of model m: add <!-- gotogether:invariants m --> ... <!-- /gotogether:invariants m -->, <!-- gotogether:actions m --> ... <!-- /gotogether:actions m -->, or <!-- gotogether:assumptions m --> ... <!-- /gotogether:assumptions m -->, each alone on its line where the table goes (unnamed markers serve a document of one model)"},
		{[]string{"-spec", "m.qnt", "-name", "other", "-write", golem + "/docs/design.md"}, "has no table of model other: add <!-- gotogether:invariants other --> ... <!-- /gotogether:invariants other -->, <!-- gotogether:actions other --> ... <!-- /gotogether:actions other -->, or <!-- gotogether:assumptions other --> ... <!-- /gotogether:assumptions other -->, each alone on its line where the table goes (unnamed markers serve a document of one model); its tables are named attempts, publication (-name picks one)"},
		{[]string{"-spec", "m.qnt", "-write", golem + "/docs/design.md", "-name", "publication", "-require", "enforced,driven"}, "invariants: -require driven is of the actions table, and " + golem + "/docs/design.md has none of model publication (<!-- gotogether:actions publication --> ... <!-- /gotogether:actions publication -->, or unnamed): add the pair, or require only enforced and checked"},
		{[]string{"-spec", "m.qnt", "-write", golem + "/docs/design.md", "-name", "publication", "-require", "targeted"}, "invariants: -require targeted is of the assumptions table, and " + golem + "/docs/design.md has none of model publication (<!-- gotogether:assumptions publication --> ... <!-- /gotogether:assumptions publication -->, or unnamed): add the pair, or require it of no assumption"},
		{[]string{"-spec", "m.qnt", "-props", "a", "-invariant", "b"}, "-props names the properties and -invariant derives them: give one"},
		{[]string{"-spec", "m.qnt", "-links", "short"}, "-links short: give line (file:line links) or name (package and function, linked to the file)"},
		{[]string{"-spec", "design.md", "-literate=false"}, "-spec design.md is Markdown and -literate=false reads no model from it: name the .qnt your tangler writes from it (design.qnt)"},
		{[]string{"-spec", "m.qnt", "-check", "none.md"}, "the document none.md:"},
		{[]string{"-spec", "m.qnt", "nowhere/..."}, "nowhere/... names no directory"},
		{[]string{"-nope"}, "invariants: flag provided but not defined: -nope (gotogether invariants -h lists its flags)"},
		{[]string{"-spec", "nowhere.qnt", fixture + "/counter"}, "the spec nowhere.qnt does not exist"},
	} {
		var out, errw bytes.Buffer
		code, err := invariants(t.Context(), &out, &errw, c.args)
		if code != 2 || err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("invariants %v: %d, %v; want exit 2 and %q", c.args, code, err, c.want)
		}
	}
	var out bytes.Buffer
	if code, err := invariants(t.Context(), &out, &out, []string{"-h"}); code != 0 || err != nil || !strings.Contains(out.String(), commands["invariants"].purpose) || !strings.Contains(out.String(), "-require") {
		t.Errorf("-h: %d, %v\n%s", code, err, out.String())
	}
}

// golem's shapes: one document, two models' named tables, each written
// and checked by its own run over the whole tree, the other model's
// qualified annotations noted and left out, its table untouched. The
// attempts run derives its properties from its invariant (-invariant
// safe: the scenario target reachedFive is no row) and spans two
// packages (state.Restore, telemetry.Call.AttemptID); publication's
// leaves its mutant witness out (-exclude) and links sites by name, so
// a line moved above them leaves its table current while attempts', by
// line, goes stale.
func TestInvariantsTwoModels(t *testing.T) {
	skip.Quint(t)
	dir := copyTree(t, golem)
	doc := filepath.Join(dir, "docs", "design.md")
	golden, _ := os.ReadFile(doc)
	// the document as it was before either table was written
	blank := regexp.MustCompile(`(?s)(<!-- gotogether:invariants (\w+) -->\n).*?(<!-- /gotogether:invariants)`).ReplaceAll(golden, []byte("${1}${3}"))
	if err := os.WriteFile(doc, blank, 0o600); err != nil {
		t.Fatal(err)
	}
	tree := dir + "/..."
	attempts := []string{"-spec", filepath.Join(dir, "model", "attempts.qnt"), "-invariant", "safe"}
	publication := []string{"-spec", filepath.Join(dir, "model", "publication.qnt"), "-exclude", "witness", "-links", "name"}
	run := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errw bytes.Buffer
		n, err := invariants(t.Context(), &out, &errw, args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return n, out.String(), errw.String()
	}
	n, out, errw := run(append(attempts, "-write", doc, tree)...)
	if n != 0 || out != doc+": written (2 properties of model attempts: 2 enforced, 2 checked; 2 actions: 1 recorded, 2 driven)\n" ||
		errw != "note: 2 annotation names of other models (qualified by modules this spec does not have, or actions recorded in files annotated only for them), left out of the tables of model attempts: publication (2)\n"+
			"note: 1 action nothing records (recorded is not required): idle\n" {
		t.Fatalf("attempts: %d\n%s%s", n, out, errw)
	}
	if now, _ := os.ReadFile(doc); !strings.Contains(string(now), "<!-- gotogether:invariants publication -->\n<!-- /gotogether:invariants publication -->") {
		t.Errorf("the attempts run wrote publication's table:\n%s", now)
	}
	if n, out, _ := run(append(publication, "-write", doc, tree)...); n != 0 || !strings.Contains(out, "written (1 property of model publication") {
		t.Fatalf("publication: %d\n%s", n, out)
	}
	if now, _ := os.ReadFile(doc); !bytes.Equal(now, golden) {
		t.Errorf("the document differs from the golden %s:\n%s", golem+"/docs/design.md", now)
	}
	// a line above every site: attempts' lines are stale, publication's
	// names are not
	edit(t, filepath.Join(dir, "state", "state.go"), "package state\n", "package state\n\n// (a line added above every site)\n")
	if n, out, _ := run(append(attempts, "-check", doc, tree)...); n != 1 || !strings.Contains(out, "stale: row noLostAttempt changed: run gotogether invariants") {
		t.Errorf("attempts, by line: %d\n%s", n, out)
	}
	if n, out, _ := run(append(publication, "-check", doc, tree)...); n != 0 || !strings.Contains(out, "ok   "+doc+": current (1 property of model publication: 1 enforced, 1 checked)") {
		t.Errorf("publication, by name: %d\n%s", n, out)
	}
}

// -props takes exactly its vals in its order, and -exclude removes from
// them; a name that is no boolean val is refused with the nearest.
func TestInvariantsProps(t *testing.T) {
	skip.Quint(t)
	var out, errw bytes.Buffer
	spec := golem + "/model/attempts.qnt"
	if n, err := invariants(t.Context(), &out, &errw, []string{"-spec", spec, "-props", "reachedFive,boundedAttempts,noLostAttempt", "-exclude", "reachedFive", golem + "/..."}); n != 0 || err != nil {
		t.Fatalf("%d, %v\n%s%s", n, err, out.String(), errw.String())
	}
	if rows := regexp.MustCompile("(?m)^\\| `(\\w+)`").FindAllStringSubmatch(out.String(), -1); len(rows) != 4 || rows[0][1] != "boundedAttempts" || rows[1][1] != "noLostAttempt" || rows[2][1] != "attempt" {
		t.Errorf("rows %v in:\n%s", rows, out.String())
	}
	if n, err := invariants(t.Context(), &out, &errw, []string{"-spec", spec, "-props", "boundedAttempt", golem + "/..."}); n != 2 || err == nil || err.Error() != "invariants: -props names boundedAttempt, which is no boolean val of model attempts: did you mean boundedAttempts? name one of its boolean vals (boundedAttempts, noLostAttempt, reachedFive, safe)" {
		t.Errorf("a misspelled -props: %d, %v", n, err)
	}
}

// golem's case (#144): publication's run over a package that holds
// attempts' connect test and directive and publication's own annotations.
// With no actions table of publication in play, no code implementing
// actions is read: -require enforced,checked passes. With one, attempts'
// driver (its Config names attempts' spec) is skipped and noted, and the
// action recorded in a file annotated only for attempts is attempts'.
func TestInvariantsOtherModelsActions(t *testing.T) {
	skip.Quint(t)
	dir := copyTree(t, golem)
	doc := filepath.Join(dir, "docs", "design.md")
	args := []string{"-spec", filepath.Join(dir, "model", "publication.qnt"), "-exclude", "witness", "-links", "name", "-require", "enforced,checked"}
	var out, errw bytes.Buffer
	if n, err := invariants(t.Context(), &out, &errw, append(args, "-check", doc, dir+"/...")); n != 0 || err != nil || strings.Contains(errw.String(), "no action of model") || strings.Contains(errw.String(), "connect") || strings.Contains(errw.String(), "note: 1 action") {
		t.Fatalf("no actions table: %d, %v\n%s%s", n, err, out.String(), errw.String())
	}
	f, err := os.OpenFile(doc, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n<!-- gotogether:actions publication -->\n<!-- /gotogether:actions publication -->\n")
	f.Close()
	out.Reset()
	errw.Reset()
	if n, err := invariants(t.Context(), &out, &errw, append(args, "-write", doc, dir+"/...")); n != 0 || err != nil ||
		!strings.Contains(errw.String(), "note: 5 annotation names of other models (qualified by modules this spec does not have, or actions recorded in files annotated only for them), left out of the tables of model publication: attempts (5)\n") ||
		!strings.Contains(errw.String(), "note: 3 connect driver entries in files whose connect.Config names another spec, left out of the actions of model publication: ../model/attempts.qnt (3)\n") {
		t.Fatalf("with publication's actions table: %d, %v\n%s%s", n, err, out.String(), errw.String())
	}
	if src, _ := os.ReadFile(doc); !strings.Contains(string(src), "| `serve` | — | — |\n<!-- /gotogether:actions publication -->") {
		t.Errorf("publication's actions:\n%s", src)
	}
}
