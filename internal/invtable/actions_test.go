package invtable

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The code that implements actions: a //quint:action directive, a
// literal Record or Invoke (on qobs, or on a recorder), and on a
// connect.Bind chain (the call itself, a variable assigned from it, a
// parameter, field or function result of its type) Action, Handle and
// Skip, and a connect.Switch's keys, each package found under any name
// it is imported as. Names through constants or variables, "_", other
// packages' Record and other types' Action are not seen.
func TestActionsOf(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a/a.go": `package a

import (
	"context"

	rec "github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
)

const name = "deposit"

// Deposit adds.
//
//quint:action action:deposit amount:n
func (b *Bank) Deposit(ctx context.Context, n int) {}

//quint:action amount:n
func (b *Bank) Untitled(n int) {}

func (b *Bank) Withdraw(ctx context.Context, r *rec.Recorder) {
	rec.Record(ctx, "withdraw", nil, "amount", 1)
	r.Invoke(ctx, "transfer")
	rec.Record(ctx, name, nil)
	qtest.Record(t, "b.yaml", nil)
}
`,
		"a/a_test.go": `package a

import (
	"testing"

	c "github.com/chucklehead-dev/gotogether/connect"
)

type other struct{}

func (other) Action(string, any) other { return other{} }

func bound() *c.Binding[*Bank] {
	return c.Bind[*Bank](nil).Action("deposit", (*Bank).Deposit).
		Returns("deposit", "balance").
		Handle("withdraw", nil)
}

func TestBank(t *testing.T) {
	b := c.Bind(newBank)
	b.Skip("audit", "init", act)
	bound().Action("transfer", nil)
	other{}.Action("nothing", nil)
	c.Test(t, c.Config{}, c.Switch{"init": nil, "close": nil, "_": nil, act: nil})
}

func more(b *c.Binding[*Bank]) { b.Handle("refund", nil) }

type driver struct{ b *c.Binding[*Bank] }

func (d driver) run() { d.b.Action("settle", nil) }
`,
	})
	anns, problems, err := Scan([]string{filepath.Join(dir, "a")})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range anns {
		if a.Kind != Records && a.Kind != Drives {
			continue
		}
		got = append(got, string(a.Kind)+" "+strings.Join(a.Names, "+")+" @"+filepath.Base(a.Site.Path)+":"+strconv.Itoa(a.Line)+"/"+strconv.Itoa(a.Site.Line)+" "+a.Site.Func+" "+a.Source)
	}
	want := []string{
		"records deposit @a.go:14/15 Bank.Deposit //quint:action action:deposit",
		`records withdraw @a.go:21/21 Bank.Withdraw rec.Record(ctx, "withdraw", ...)`,
		`records transfer @a.go:22/22 Bank.Withdraw Invoke(ctx, "transfer", ...)`,
		`drives deposit @a_test.go:14/14 bound .Action("deposit", ...)`,
		`drives withdraw @a_test.go:16/16 bound .Handle("withdraw", ...)`,
		`drives audit @a_test.go:21/21 TestBank .Skip("audit", ...)`,
		`drives init @a_test.go:21/21 TestBank .Skip("init", ...)`,
		`drives transfer @a_test.go:22/22 TestBank .Action("transfer", ...)`,
		`drives init @a_test.go:24/24 TestBank c.Switch{"init": ...}`,
		`drives close @a_test.go:24/24 TestBank c.Switch{"close": ...}`,
		`drives refund @a_test.go:27/27 more .Handle("refund", ...)`,
		`drives settle @a_test.go:31/31 driver.run .Action("settle", ...)`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("implementations\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(problems) != 1 || !strings.HasSuffix(problems[0], "a.go:17: //quint:action on Bank.Untitled names no action: give it action:NAME, the model's action it records (//quint:action action:deposit ...)") {
		t.Errorf("problems %q", problems)
	}
}

// Each implementation goes in its action's row; init and step are no
// row and no error; any other name is an error at its line with the
// nearest action.
func TestActionRows(t *testing.T) {
	m := read(t, "m")
	m.Actions = []*Property{{Name: "deposit"}, {Name: "withdraw"}, {Name: "audit"}}
	anns := []Annotation{
		{Kind: Records, Line: 3, Site: Site{Path: "b.go", Line: 4, Func: "B.Deposit"}, Names: []string{"deposit"}, Source: "//quint:action action:deposit"},
		{Kind: Records, Line: 9, Site: Site{Path: "b.go", Line: 9, Func: "B.Withdraw"}, Names: []string{"depsoit"}, Source: `qobs.Record(ctx, "depsoit", ...)`},
		{Kind: Drives, Line: 5, Site: Site{Path: "b_test.go", Line: 5, Func: "TestB"}, Names: []string{"deposit"}, Source: `.Action("deposit", ...)`},
		{Kind: Drives, Line: 6, Site: Site{Path: "b_test.go", Line: 6, Func: "TestB"}, Names: []string{"init"}, Source: `connect.Switch{"init": ...}`},
		{Kind: Drives, Line: 7, Site: Site{Path: "b_test.go", Line: 7, Func: "TestB"}, Names: []string{"withdrew"}, Source: `.Skip("withdrew", ...)`},
	}
	table, errs := m.Table(anns)
	wantErrs := []string{
		`b.go:9: qobs.Record(ctx, "depsoit", ...) names depsoit, which is no action of model m: did you mean deposit? name one of its actions (deposit, withdraw, audit), or leave code of another model out of the patterns`,
		`b_test.go:7: .Skip("withdrew", ...) names withdrew, which is no action of model m: did you mean withdraw? name one of its actions (deposit, withdraw, audit), or leave code of another model out of the patterns`,
	}
	if !slices.Equal(errs, wantErrs) {
		t.Errorf("errors\n%s", strings.Join(errs, "\n"))
	}
	want := "| action | recorded by | driven by |\n|---|---|---|\n" +
		"| `deposit` | `B.Deposit` [b.go:4](b.go#L4) | `TestB` [b_test.go:5](b_test.go#L5) |\n" +
		"| `withdraw` | — | — |\n" +
		"| `audit` | — | — |\n"
	if got := table.MarkdownActions(""); got != want {
		t.Errorf("table\n%s\nwant\n%s", got, want)
	}
	if w := table.Warnings("driven"); !slices.Equal(w, []string{
		`action withdraw: nothing drives it: bind it in a connect driver (connect.Bind(...).Action("withdraw", ...), Handle or Skip, or a connect.Switch key)`,
		`action audit: nothing drives it: bind it in a connect driver (connect.Bind(...).Action("audit", ...), Handle or Skip, or a connect.Switch key)`,
	}) {
		t.Errorf("driven warnings %q", w)
	}
	if w := table.Warnings("recorded"); len(w) != 2 || !strings.HasPrefix(w[0], "action withdraw: nothing records it: put //quint:action action:withdraw on the function that does it") {
		t.Errorf("recorded warnings %q", w)
	}
	if w := table.Warnings("enforced", "checked"); slices.ContainsFunc(w, func(s string) bool { return strings.HasPrefix(s, "action") }) {
		t.Errorf("property warnings name actions: %q", w)
	}
}
