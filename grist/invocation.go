package grist

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/grist/internal/rec"
)

// An Invocation is a command with its arguments and the outcome expected
// of it: a step of an acceptance scenario (gristtest.Scenario), written
// from the catalog's own values, `Add.With(AddItem{Item: "a", Qty: 2})`,
// `Remove.With(RemoveItem{Item: "a", Qty: 3}).Refused(ErrShort)`.
type Invocation struct {
	// Command is the command's name, Action its model action's.
	Command, Action string
	// Outcome is "ok", or the name of the refusal Refused expects.
	Outcome string
	// Params are the model action's parameters the arguments give, in
	// field order (a field's JSON name, renamed where Quint reserves it,
	// as the generated model names it), and Values each one's Quint
	// literal.
	Params, Values []string
	// refuses are the command's declared refusals, its rules' too
	refuses []string
}

// With is the command c invoked with args, expected to commit (ok;
// Refused expects a refusal). A field the model does not take (tagged
// `grist:"-"`) is left out.
func (c *Command[C]) With(args C) Invocation {
	inv := Invocation{Command: c.info.name, Action: c.info.quint, Outcome: "ok"}
	for _, b := range c.app.Catalog().Commands {
		if b.Name == c.info.name {
			inv.refuses = b.Refuses
		}
	}
	fs, err := scalarFields(c.info.args)
	if err != nil {
		panic(fmt.Sprintf("grist: %s.With: %v", c.info.name, err))
	}
	v := reflect.ValueOf(args)
	byJSON := map[string]reflect.Value{}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if f := t.Field(i); f.IsExported() {
			byJSON[rec.JSONName(f)] = v.Field(i)
		}
	}
	for _, f := range fs {
		inv.Params = append(inv.Params, f.param)
		inv.Values = append(inv.Values, quintLiteral(byJSON[f.json]))
	}
	return inv
}

// Refused is inv expecting the refusal err (a grist.Rejection the command
// or one of its rules declares) instead of a commit. Any other error
// panics, naming the command's refusals.
func (inv Invocation) Refused(err error) Invocation {
	var r *Rejected
	if !errors.As(err, &r) {
		panic(fmt.Sprintf("grist: %s.With(...).Refused(%v): no grist.Rejection: name one of its refusals (%s)", inv.Command, err, strings.Join(inv.refuses, ", ")))
	}
	if !slices.Contains(inv.refuses, r.Name) {
		panic(fmt.Sprintf("grist: %s.With(...).Refused(%q): the command declares no such refusal, nor do its rules (its refusals: %s)", inv.Command, r.Name, strings.Join(inv.refuses, ", ")))
	}
	inv.Outcome = r.Name
	return inv
}

// String is the invocation as a scenario's report names it:
// add-item(item = "a", qty = 2) refused full.
func (inv Invocation) String() string {
	parts := make([]string, len(inv.Params))
	for i := range inv.Params {
		parts[i] = inv.Params[i] + " = " + inv.Values[i]
	}
	s := inv.Command + "(" + strings.Join(parts, ", ") + ")"
	if inv.Outcome != "ok" {
		s += " refused " + inv.Outcome
	}
	return s
}

// quintLiteral is a scalar argument as a Quint literal.
func quintLiteral(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return strconv.Quote(v.String())
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	}
	return fmt.Sprint(v.Interface())
}
