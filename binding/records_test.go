package binding

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Literal Record actions are found in files that import qobs (tests
// too), whatever the package's local name; actions through a constant
// are not.
func TestScanRecords(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app.go", `package app

import (
	"context"

	obs "github.com/chucklehead-dev/gotogether/qobs"
)

const settle = "settle"

func deposit(ctx context.Context, r *obs.Recorder) {
	obs.Record(ctx, "deposit", nil, "amount", 1)
	r.Record(ctx, "withdraw", nil)
	obs.Record(ctx, settle, nil)
	obs.Record(ctx, `+"`depositt`"+`, nil)
}

var _ = func(ctx context.Context) { obs.Record(ctx, "init", nil) }
`)
	write("app_test.go", `package app

import (
	"context"

	"testing"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
)

func record(ctx context.Context) { qobs.Record(ctx, "transfer", nil); qobs.Invoke(ctx, "invokd").Done(nil) }

func TestRun(t *testing.T) { qtest.Record(t, "bank.yaml", nil) }
`)
	write("other.go", `package app

func other(l logger) { l.Record(nil, "deposit", nil) }
`)
	recs, err := ScanRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range recs {
		m := ""
		if r.Method {
			m = " (method)"
		}
		got = append(got, r.Pos[strings.LastIndex(r.Pos, "/")+1:]+" "+r.Func+" "+r.Action+m)
	}
	want := []string{
		"app.go:12:18 deposit deposit",
		"app.go:13:16 deposit withdraw (method)",
		"app.go:15:18 deposit depositt",
		"app.go:18:53  init",
		"app_test.go:12:53 record transfer",
		"app_test.go:12:88 record invokd",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func bankModel() *Model {
	return &Model{Module: "bank", Actions: map[string][]Param{
		"init": nil, "step": nil, "deposit": nil, "withdraw": nil, "transfer": nil,
	}}
}

// An unknown literal action is reported with the nearest action and the
// model's list.
func TestLintRecords(t *testing.T) {
	src := `package app

import "github.com/chucklehead-dev/gotogether/qobs"

func f() {
	qobs.Record(nil, "deposit", nil)
	qobs.Record(nil, "depositt", nil)
	qobs.Record(nil, "close", nil)
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "app.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	errs := LintRecords(bankModel(), RecordsOf(fset, f))
	want := []string{
		`app.go:7:19: f: Record(ctx, "depositt", ...): depositt is not an action of bank (did you mean deposit?); its actions: deposit, transfer, withdraw`,
		`app.go:8:19: f: Record(ctx, "close", ...): close is not an action of bank; its actions: deposit, transfer, withdraw`,
	}
	if strings.Join(errs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(errs, "\n"), strings.Join(want, "\n"))
	}
}

// A step recording a field under a name Quint keeps (item) for a model
// whose parameter is renamed (item_) needs the binding's mapping: the
// lint names it, and the mapping clears it. A newcomer's first model met
// QNT101 on item, then a parameter reading a field no step had.
func TestLintReservedField(t *testing.T) {
	src := `package app

import "github.com/chucklehead-dev/gotogether/qobs"

func f() {
	qobs.Record(nil, "reserve", nil, "item", "apple", "qty", 1, "obs.left", 2)
	qobs.Invoke(nil, "reserve", "item", "pear", "qty", 1)
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "app.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{Module: "stock", Actions: map[string][]Param{"init": nil, "step": nil,
		"reserve": {{Name: "item_", Type: "str"}, {Name: "qty", Type: "int"}}}}
	b := &Binding{Actions: map[string]Action{"reserve": {}}}
	errs, _ := b.LintSource(m, nil, RecordsOf(fset, f))
	want := `app.go:6:19: Record(ctx, "reserve", ...) records the field item, a Quint built-in's name, which the model's parameter cannot take: its item_ reads a field item_ by default, which no step records: map it in the binding (actions: reserve: args: item_: '{{.item}}')`
	if len(errs) != 2 || errs[0] != want || !strings.HasPrefix(errs[1], "app.go:7:19: ") {
		t.Fatalf("errs:\n%s\nwant first:\n%s", strings.Join(errs, "\n"), want)
	}
	b.Actions["reserve"] = Action{Args: map[string]string{"item_": "{{.item}}"}}
	if errs, _ := b.LintSource(m, nil, RecordsOf(fset, f)); len(errs) != 0 {
		t.Fatalf("mapped, still: %q", errs)
	}
}

// LintSource counts an action a literal Record emits as emitted.
func TestLintSourceEmitted(t *testing.T) {
	b := &Binding{Actions: map[string]Action{"deposit": {}, "withdraw": {}}}
	recs := []RecordCall{{Pos: "app.go:1", Action: "deposit"}}
	errs, warns := b.LintSource(bankModel(), nil, recs)
	if len(errs) != 0 || len(warns) != 1 || !strings.HasPrefix(warns[0], "action withdraw is bound but neither") {
		t.Fatalf("errs %q, warns %q", errs, warns)
	}
}

// One source tree, two bindings (a system's leadership and its clients'
// history): a step for either is no error, one for neither is, and each
// binding's actions nothing emits are warned of. Against one binding
// alone the other's steps were errors.
func TestLintSourcesOfTwoBindings(t *testing.T) {
	bank := Bound{Binding: &Binding{Actions: map[string]Action{"deposit": {}, "withdraw": {}}}, Model: bankModel()}
	cluster := Bound{Binding: &Binding{Actions: map[string]Action{"elect": {}, "heal": {}}},
		Model: &Model{Module: "cluster", Actions: map[string][]Param{"init": nil, "step": nil, "elect": nil, "heal": nil}}}
	recs := []RecordCall{{Pos: "app.go:1", Action: "deposit"}, {Pos: "app.go:2", Action: "elect"}, {Pos: "app.go:3", Action: "close"}}
	anns := []Annotation{{Pos: "app.go:4", Func: "Heal", Action: "heal"}}
	errs, warns := LintSources([]Bound{bank, cluster}, anns, recs)
	if len(errs) != 1 || !strings.Contains(errs[0], `"close"`) || !strings.Contains(errs[0], "none of bank, cluster") {
		t.Errorf("errors %q, want close's alone", errs)
	}
	if len(warns) != 1 || !strings.HasPrefix(warns[0], "bank: action withdraw") {
		t.Errorf("warnings %q, want bank's withdraw alone", warns)
	}
	if errs, _ := bank.Binding.LintSource(bank.Model, anns, recs); len(errs) != 3 {
		t.Errorf("against bank alone: %q, want elect, heal and close", errs)
	}
}
