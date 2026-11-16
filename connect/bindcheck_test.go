package connect

import (
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
	"github.com/chucklehead-dev/gotogether/itf"
)

// Types for the fit table: what itf.Decode fills from Quint's values.
type (
	fitAcct struct {
		Name    string
		Limit   int `itf:"limit"`
		Ignored bool
		//lint:ignore U1000 unexported on purpose: the fit table checks Decode skips it
		internal int
	}
	fitWrongAcct struct{ Limit string }
	fitCmd       struct {
		Tag   string `itf:",tag"`
		Value any    `itf:",value"`
	}
	fitCmdTyped struct {
		Tag   string `itf:",tag"`
		Value string `itf:",value"`
	}
	fitCmdFields struct { // a field per variant
		Open  *string
		Close bool
	}
	fitPair struct {
		N int
		S string
	}
	fitPhase string
	fitSelf  struct {
		Kids []fitSelf
	}
	fitDecodes struct{}
)

func (*fitDecodes) UnmarshalITF(itf.Value) error { return nil }

// The fit rules accept what itf.Decode decodes: each case's sample
// value of the Quint type decodes into the Go type exactly when the
// check says the type fits.
func TestBindFitsAsDecodes(t *testing.T) {
	var (
		qint  = &qeval.Type{Kind: "int"}
		qstr  = &qeval.Type{Kind: "str"}
		qbool = &qeval.Type{Kind: "bool"}
		unit  = &qeval.Type{Kind: "tup"}
		list  = func(e *qeval.Type) *qeval.Type { return &qeval.Type{Kind: "list", Elem: e} }
		set   = func(e *qeval.Type) *qeval.Type { return &qeval.Type{Kind: "set", Elem: e} }
		fun   = func(k, v *qeval.Type) *qeval.Type { return &qeval.Type{Kind: "fun", Arg: k, Res: v} }
		rec   = func(fs ...qeval.Field) *qeval.Type { return &qeval.Type{Kind: "rec", Fields: fs} }
		tup   = func(es ...*qeval.Type) *qeval.Type {
			t := &qeval.Type{Kind: "tup"}
			for i, e := range es {
				t.Fields = append(t.Fields, qeval.Field{Name: string(rune('0' + i)), Type: e})
			}
			return t
		}
		sum = func(fs ...qeval.Field) *qeval.Type { return &qeval.Type{Kind: "sum", Fields: fs} }
		f   = func(n string, t *qeval.Type) qeval.Field { return qeval.Field{Name: n, Type: t} }
	)
	acct := rec(f("name", qstr), f("limit", qint))
	acctV := itf.Value{Kind: itf.Record, Fields: map[string]itf.Value{"name": itf.StrOf("x"), "limit": itf.IntOf(1)}}
	option := sum(f("Some", qint), f("None", unit))
	cmd := sum(f("Open", qstr), f("Close", unit))
	phase := sum(f("Idle", unit), f("Busy", unit))
	self := rec() // { kids: List[self] }: a cycle the check must stop at
	self.Fields = []qeval.Field{f("kids", list(self))}
	for _, c := range []struct {
		name string
		q    *qeval.Type
		v    itf.Value // a value of q
		t    reflect.Type
		fits bool
	}{
		{"int into int", qint, itf.IntOf(3), reflect.TypeFor[int](), true},
		{"int into uint8", qint, itf.IntOf(3), reflect.TypeFor[uint8](), true},
		{"int into float64", qint, itf.IntOf(3), reflect.TypeFor[float64](), true},
		{"int into big.Int", qint, itf.IntOf(3), reflect.TypeFor[big.Int](), true},
		{"int into *big.Int", qint, itf.IntOf(3), reflect.TypeFor[*big.Int](), true},
		{"int into string", qint, itf.IntOf(3), reflect.TypeFor[string](), false},
		{"str into int", qstr, itf.StrOf("a"), reflect.TypeFor[int](), false},
		{"str into []byte", qstr, itf.StrOf("a"), reflect.TypeFor[[]byte](), true},
		{"bool into bool", qbool, itf.BoolOf(true), reflect.TypeFor[bool](), true},
		{"bool into int", qbool, itf.BoolOf(true), reflect.TypeFor[int](), false},
		{"list into slice", list(qstr), itf.ListOf(itf.StrOf("a")), reflect.TypeFor[[]string](), true},
		{"list into slice of int", list(qstr), itf.ListOf(itf.StrOf("a")), reflect.TypeFor[[]int](), false},
		{"set into slice", set(qint), itf.SetOf(itf.IntOf(1)), reflect.TypeFor[[]int](), true},
		{"set into map[T]bool", set(qint), itf.SetOf(itf.IntOf(1)), reflect.TypeFor[map[int]bool](), true},
		{"set into map[T]int", set(qint), itf.SetOf(itf.IntOf(1)), reflect.TypeFor[map[int]int](), false},
		{"map into map", fun(qstr, qint), itf.Value{Kind: itf.Map, Pairs: [][2]itf.Value{{itf.StrOf("a"), itf.IntOf(1)}}}, reflect.TypeFor[map[string]int](), true},
		{"map into map of wrong keys", fun(qstr, qint), itf.Value{Kind: itf.Map, Pairs: [][2]itf.Value{{itf.StrOf("a"), itf.IntOf(1)}}}, reflect.TypeFor[map[int]int](), false},
		{"map into slice of pairs", fun(qstr, qint), itf.Value{Kind: itf.Map, Pairs: [][2]itf.Value{{itf.StrOf("a"), itf.IntOf(1)}}}, reflect.TypeFor[[]struct {
			K string
			V int
		}](), true},
		{"map into int", fun(qstr, qint), itf.Value{Kind: itf.Map, Pairs: [][2]itf.Value{{itf.StrOf("a"), itf.IntOf(1)}}}, reflect.TypeFor[int](), false},
		{"record into struct", acct, acctV, reflect.TypeFor[fitAcct](), true},
		{"record into struct of a wrong field", acct, acctV, reflect.TypeFor[fitWrongAcct](), false},
		{"record into map[string]any", acct, acctV, reflect.TypeFor[map[string]any](), true},
		{"record into map[string]int", acct, acctV, reflect.TypeFor[map[string]int](), false},
		{"record into string", acct, acctV, reflect.TypeFor[string](), false},
		{"tuple into struct", tup(qint, qstr), itf.TupleOf(itf.IntOf(1), itf.StrOf("a")), reflect.TypeFor[fitPair](), true},
		{"tuple into a struct of other arity", tup(qint), itf.TupleOf(itf.IntOf(1)), reflect.TypeFor[fitPair](), false},
		{"tuple into array", tup(qint, qint), itf.TupleOf(itf.IntOf(1), itf.IntOf(2)), reflect.TypeFor[[2]int](), true},
		{"tuple into array of other length", tup(qint, qint), itf.TupleOf(itf.IntOf(1), itf.IntOf(2)), reflect.TypeFor[[3]int](), false},
		{"tuple into slice", tup(qint, qstr), itf.TupleOf(itf.IntOf(1), itf.StrOf("a")), reflect.TypeFor[[]any](), true},
		{"option into pointer", option, itf.Some(itf.IntOf(1)), reflect.TypeFor[*int](), true},
		{"option into pointer of string", option, itf.Some(itf.IntOf(1)), reflect.TypeFor[*string](), false},
		{"enum into string kind", phase, itf.Enum("Busy"), reflect.TypeFor[fitPhase](), true},
		{"sum with a payload into string", cmd, itf.VariantOf("Open", itf.StrOf("a")), reflect.TypeFor[string](), false},
		{"sum into tag and value", cmd, itf.VariantOf("Open", itf.StrOf("a")), reflect.TypeFor[fitCmd](), true},
		{"sum into a typed value", cmd, itf.VariantOf("Open", itf.StrOf("a")), reflect.TypeFor[fitCmdTyped](), true},
		{"sum into a field per variant", cmd, itf.VariantOf("Open", itf.StrOf("a")), reflect.TypeFor[fitCmdFields](), true},
		{"sum into a struct without its variant", cmd, itf.VariantOf("Open", itf.StrOf("a")), reflect.TypeFor[fitPair](), false},
		{"recursive type", self, itf.Value{Kind: itf.Record, Fields: map[string]itf.Value{"kids": itf.ListOf()}}, reflect.TypeFor[fitSelf](), true},
		// accepted, unsure or anything
		{"any", acct, acctV, reflect.TypeFor[any](), true},
		{"itf.Value", acct, acctV, reflect.TypeFor[itf.Value](), true},
		{"an Unmarshaler", qint, itf.IntOf(1), reflect.TypeFor[fitDecodes](), true},
		{"a type variable", &qeval.Type{Kind: "var", Name: "a"}, itf.IntOf(1), reflect.TypeFor[int](), true},
		{"an uninterpreted type", &qeval.Type{Kind: "const", Name: "T"}, itf.StrOf("x"), reflect.TypeFor[string](), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, why, ok := fitsAny([]*qeval.Type{c.q}, c.t)
			if ok != c.fits {
				t.Errorf("fits %s into %s: %v (%s), want %v", c.q, c.t, ok, why, c.fits)
			}
			if c.q.Kind == "var" || c.q.Kind == "const" {
				return // the sample is one value of many
			}
			err := itf.Decode(c.v, reflect.New(c.t).Interface())
			if (err == nil) != c.fits {
				t.Errorf("itf.Decode of %s into %s: %v, but the check says fits=%v: the rules differ", c.v, c.t, err, c.fits)
			}
		})
	}
}

// The bank's bindings checked against bank.qnt's actions, nondets and
// types, before any trace: the faithful binding fits, each mismatch is a
// line naming the action, the pick and both types, and the fix, and a
// binding with several has them all.
func TestBindCheckedAgainstModel(t *testing.T) {
	r := sharedBankRun(t) // the model compiled already: no quint here
	check := func(b *Binding[*bank]) []string {
		t.Helper()
		ft := &fatalTB{TB: t}
		d, ok := b.Driver(ft).(modelChecked)
		if !ok || ft.fatal != "" {
			t.Fatalf("a bound driver is checked against the model: %T %s", d, ft.fatal)
		}
		bad, _ := d.checkModel(r.m, r.c)
		return bad
	}
	newBank := func(testing.TB) *bank { return &bank{balances: map[string]int{}} }
	if bad := check(bankBinding(false, (*bank).deposit)); len(bad) > 0 {
		t.Errorf("the faithful binding: %v", bad)
	}
	if bad := check(bankBinding(false, (*bank).depositBalance).Returns("deposit", "balanceOf", "account")); len(bad) > 0 {
		t.Errorf("the faithful binding with Returns: %v", bad)
	}
	// the choice deposit's account is, observed: a str, as the model's
	if bad := check(Bind(newBank).Action("deposit", func(b *bank, a string, n int) string { b.deposit(a, n); return a }, "account", "amount").Observes("deposit", "account")); len(bad) > 0 {
		t.Errorf("an observed pick of its type: %v", bad)
	}
	// optional (a pointer) and an interface: accepted
	if bad := check(Bind(newBank).Action("deposit", func(b *bank, a *string, n any) {}, "account", "amount")); len(bad) > 0 {
		t.Errorf("a pointer and an interface: %v", bad)
	}
	for _, c := range []struct {
		name string
		b    *Binding[*bank]
		want []string
	}{
		{"a typo'd action", Bind(newBank).Action("depositt", (*bank).deposit, "account", "amount"),
			[]string{"connect.Bind: action depositt: the model has no action depositt (its actions: deposit, transfer, withdraw): bind each action by the model's name for it"}},
		{"a skipped action the model lacks", Bind(newBank).Skip("aliceSavesTest"),
			[]string{"connect.Bind: action aliceSavesTest: the model has no action aliceSavesTest (its actions: deposit, transfer, withdraw)"}},
		{"a typo'd pick", Bind(newBank).Action("deposit", (*bank).deposit, "acount", "amount"),
			[]string{"connect.Bind: action deposit, parameter 1 (pick acount): the model has no nondet or parameter acount (its picks: account, amount, dst, src): name the nondet that feeds the parameter"}},
		{"a wrong Go type", Bind(newBank).Action("deposit", func(*bank, string, string) {}, "account", "amount"),
			[]string{"connect.Bind: action deposit, parameter 2 (pick amount): Go type string does not fit the model's int (int does not decode into string): make the parameter a type itf.Decode fills from int"}},
		{"a Returns def the model lacks", bankBinding(false, (*bank).depositBalance).Returns("deposit", "balanceOff", "account"),
			[]string{"connect.Bind: action deposit: Returns' def balanceOff is no val or def of testdata/bank.qnt: name the model definition the result must equal"}},
		{"a Returns def of another arity", bankBinding(false, (*bank).depositBalance).Returns("deposit", "balanceOf"),
			[]string{"connect.Bind: action deposit: Returns' def balanceOf takes 1 argument (a), but 0 picks are given: name one pick per parameter, in order"}},
		{"a Returns def of another type", bankBinding(false, func(b *bank, a string, n int) string { b.deposit(a, n); return a }).Returns("deposit", "balanceOf", "account"),
			[]string{"connect.Bind: action deposit: its result, Go type string, does not fit Returns' def balanceOf, of the model's int (int does not decode into string): return a type itf.Decode fills from int"}},
		{"an Observes type mismatch", Bind(newBank).Action("deposit", func(b *bank, a string, n int) int { b.deposit(a, n); return 0 }, "account", "amount").Observes("deposit", "account"),
			[]string{"connect.Bind: action deposit: Observes' pick account: result 1, Go type int, does not fit the model's str (str does not decode into int): return a type itf.Decode fills from str"}},
		{"several", Bind(newBank).
			Action("deposit", func(*bank, string, string) {}, "account", "amount").
			Action("withdraw", (*bank).withdraw, "acount", "amount").
			Action("transfre", (*bank).transfer, "src", "dst", "amount"),
			[]string{
				"connect.Bind: action deposit, parameter 2 (pick amount): Go type string does not fit the model's int",
				"connect.Bind: action withdraw, parameter 1 (pick acount): the model has no nondet or parameter acount",
				"connect.Bind: action transfre: the model has no action transfre",
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			bad := check(c.b)
			if len(bad) != len(c.want) {
				t.Fatalf("%d mismatches, want %d:\n%s", len(bad), len(c.want), strings.Join(bad, "\n"))
			}
			for i, w := range c.want {
				if !strings.HasPrefix(bad[i], w) {
					t.Errorf("mismatch %d:\n  got  %s\n  want %s", i+1, bad[i], w)
				}
			}
		})
	}
}

// connect.Run checks a bound driver's binding before replaying a trace,
// and fails with every mismatch at once: the implementation is never
// made.
func TestBindCheckFailsRun(t *testing.T) {
	needQuint(t)
	out := connectFailureOutput(t, "bind-mismatched")
	for _, want := range []string{
		"connect: the binding does not fit testdata/bank.qnt (2 mismatches, found before any trace ran):",
		"    connect.Bind: action deposit, parameter 2 (pick amount): Go type string does not fit the model's int",
		"    connect.Bind: action transfre: the model has no action transfre",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "implementation made") {
		t.Errorf("the implementation was made, a trace replayed:\n%s", out)
	}
}
