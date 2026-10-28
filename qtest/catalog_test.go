package qtest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A Catalog's Exercised wants each of its Takes but those excepted, by
// name or by action, naming each one missed with its hint; an exception
// naming nothing of it panics with its names, and Take finds one by name.
func TestCatalogExercised(t *testing.T) {
	t.Parallel()
	step := func(a, o string) binding.Call {
		return binding.Call{Action: a, Step: &qtrace.Step{Action: a, Outcome: o}}
	}
	runs := []*Run{{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass,
		Calls: []binding.Call{{Action: "init"}, step("borrow", "ok"), step("borrow", "unavailable")}}}}}}
	model := map[string]bool{"init": true, "step": true, "borrow": true, "receipts": true}
	c := Catalog{Name: "library", Takes: []Take{
		Takes("borrow").Outcome("ok"), Takes("borrow").Outcome("unavailable"),
		Takes("borrow").Outcome("limit").Hint("send borrows past the limit"),
		Takes("receipts").Hint("commit a borrow"),
	}}
	for _, x := range []struct {
		except []string
		want   string // in the message; "" for a pass
	}{
		{nil, "2 of 4 not taken:\n  borrow with outcome limit (0 of 1 runs): send borrows past the limit\n  receipts (0 of 1 runs): commit a borrow"},
		{[]string{"borrow.limit"}, "1 of 3 not taken:\n  receipts (0 of 1 runs): commit a borrow"},
		{[]string{"borrow.limit", "receipts"}, ""},
	} {
		msg := c.Exercised(x.except...).judgeRuns(runs, model)
		switch {
		case x.want == "" && msg != "":
			t.Errorf("except %v: %q, want a pass", x.except, msg)
		case x.want != "" && !strings.Contains(msg, x.want):
			t.Errorf("except %v: %q, want %q in it", x.except, msg, x.want)
		}
	}
	if k := c.Take("borrow.limit"); k.hint != "send borrows past the limit" || k.Name() != "borrow.limit" {
		t.Errorf("Take(borrow.limit) = %+v", k)
	}
	twice := Catalog{Name: "library", Takes: append(c.Takes, Takes("borrow").Outcome("limit").With("n", "3"))}
	for _, x := range []struct {
		f    func()
		want string
	}{
		{func() { c.Exercised("borow") }, `"borow" names no block of the catalog (its names: borrow, borrow.limit`},
		{func() { c.Take("borrow.late") }, "no such block (its takes: borrow.ok, borrow.unavailable, borrow.limit, receipts)"},
		{func() { c.Exercised("borrow", "receipts") }, "Catalog(library).Exercised(borrow, receipts): no Take left to want"},
		{func() { Catalog{Name: "library"}.Exercised() }, "Catalog(library).Exercised(): no Take left to want"},
		{func() { twice.Take("borrow.limit") }, "2 Takes of that name"},
	} {
		msg := func() (msg string) {
			defer func() { msg = fmt.Sprint(recover()) }()
			x.f()
			return ""
		}()
		if !strings.Contains(msg, x.want) {
			t.Errorf("panic %q, want %q", msg, x.want)
		}
	}
}
