package bank

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A production run sanitized before it is kept: account names become
// pseudonyms (equal names, equal pseudonyms) and every field the binding
// does not read is dropped. It still conforms, checked against the model
// instance whose accounts are the pseudonyms.
func TestSanitizedRunConforms(t *testing.T) {
	qtest.RequireQuint(t)
	run := qtest.Record(t, "bank.binding.yaml", func(*qobs.Recorder) { payday(New(accounts)) }, qtest.Global())
	ch, err := validate.NewChecker("bank.binding.yaml", validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := qtrace.Filter{Pseudonymize: []string{"account", "src", "dst"}, Salt: "test", Keep: ch.Binding.UsedFields(ch.Model)}
	clean := f.Apply(run.Steps)
	var names []string
	for _, a := range accounts {
		names = append(names, fmt.Sprintf("%q", f.Pseudonym(a)))
	}
	for _, s := range clean {
		if strings.Contains(fmt.Sprint(s.Args, s.Obs, s.Error), "alice") {
			t.Fatalf("a name survived: %+v", s)
		}
	}
	qtest.Validate(t, "bank.binding.yaml", clean, qtest.Constants(map[string]string{"ACCOUNTS": "Set(" + strings.Join(names, ", ") + ")"})).
		Expect(t, qtest.Conforms())
}
