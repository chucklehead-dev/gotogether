package itf

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"
)

// bigNumberJSON is n in bignumber.js's form, as quint 0.33's Rust backend
// writes it (each number an ITF #bigint): the coefficient's decimal
// digits, the first limb holding e%14+1 of them and every other 14,
// trailing zero limbs dropped.
func bigNumberJSON(n *big.Int) string {
	sign, ds := 1, new(big.Int).Abs(n).String()
	if n.Sign() < 0 {
		sign = -1
	}
	exp := len(ds) - 1
	lead := exp%14 + 1
	limbs := []string{ds[:lead]}
	for rest := ds[lead:]; rest != ""; rest = rest[14:] {
		limbs = append(limbs, rest[:14])
	}
	for len(limbs) > 1 && strings.Trim(limbs[len(limbs)-1], "0") == "" {
		limbs = limbs[:len(limbs)-1]
	}
	parts := make([]string, len(limbs))
	for i, l := range limbs {
		if l = strings.TrimLeft(l, "0"); l == "" {
			l = "0"
		}
		parts[i] = fmt.Sprintf(`{"#bigint": %q}`, l)
	}
	return fmt.Sprintf(`{"s": {"#bigint": "%d"}, "e": {"#bigint": "%d"}, "c": [%s]}`, sign, exp, strings.Join(parts, ", "))
}

func object(t *testing.T, s string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func bigOf(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic(s)
	}
	return n
}

// Integers at and around 2^53, 2^63 and far past them, and those whose
// limbs are zero within or at the end, read back from bignumber.js's form
// as the integers they are; the forms quint's Rust backend wrote (the
// fixture's) are read as bignumber.js's documentation says.
func TestBigNumberRoundTrip(t *testing.T) {
	two := big.NewInt(2)
	pow := func(b *big.Int, e int64) *big.Int { return new(big.Int).Exp(b, big.NewInt(e), nil) }
	add := func(a *big.Int, d int64) *big.Int { return new(big.Int).Add(a, big.NewInt(d)) }
	neg := func(a *big.Int) *big.Int { return new(big.Int).Neg(a) }
	var ns []*big.Int
	for _, base := range []*big.Int{pow(two, 53), pow(two, 63), pow(big.NewInt(10), 14), pow(big.NewInt(10), 15),
		pow(big.NewInt(10), 28), pow(two, 64), pow(two, 200), pow(big.NewInt(10), 100)} {
		for _, d := range []int64{-2, -1, 0, 1, 2} {
			ns = append(ns, add(base, d), neg(add(base, d)))
		}
	}
	ns = append(ns, big.NewInt(0), big.NewInt(1), big.NewInt(-1), bigOf("100000000000000000000000000001"), bigOf("123400000000000000000000000000000"))
	for _, n := range ns {
		src := bigNumberJSON(n)
		got, ok := bigNumber(object(t, src))
		if !ok || got.Cmp(n) != 0 {
			t.Errorf("%s: read as %v (%v), want %s", src, got, ok, n)
		}
	}
	// as quint wrote them (bignumber.rust.itf.json), and as bignumber.js
	// documents its form: -2^63 is c [92233, 72036854775808], e 18, s -1
	for src, want := range map[string]string{
		`{"s": {"#bigint": "-1"}, "e": {"#bigint": "18"}, "c": [{"#bigint": "92233"}, {"#bigint": "72036854775808"}]}`: "-9223372036854775808",
		`{"s": {"#bigint": "1"}, "e": {"#bigint": "18"}, "c": [{"#bigint": "92233"}, {"#bigint": "72036854775807"}]}`:  "9223372036854775807",
		`{"s": {"#bigint": "1"}, "e": {"#bigint": "15"}, "c": [{"#bigint": "90"}, {"#bigint": "7199254740993"}]}`:      "9007199254740993",
		`{"s": {"#bigint": "1"}, "e": {"#bigint": "16"}, "c": [{"#bigint": "100"}, {"#bigint": "1"}]}`:                 "10000000000000001",
		`{"s": {"#bigint": "1"}, "e": {"#bigint": "17"}, "c": [{"#bigint": "1200"}]}`:                                  "120000000000000000",
		`{"s": {"#bigint": "-1"}, "e": {"#bigint": "14"}, "c": [{"#bigint": "1"}]}`:                                    "-100000000000000",
		`{"s": 1, "e": 18, "c": [46116, 86018427387904]}`:                                                              "4611686018427387904",
		`{"s": 1, "e": 0, "c": [0]}`: "0",
	} {
		if got, ok := bigNumber(object(t, src)); !ok || got.String() != want {
			t.Errorf("%s: read as %v (%v), want %s", src, got, ok, want)
		}
	}
}

// An object that is not of bignumber.js's form for an integer is a
// record, read as one with no type: only the form itself is ambiguous.
func TestBigNumberFormOnly(t *testing.T) {
	for _, src := range []string{
		`{"s": 2, "e": 18, "c": [46116, 86018427387904]}`,   // a sign is 1 or -1
		`{"s": 1, "e": 18, "c": [4611, 86018427387904]}`,    // the first limb holds e%14+1 digits
		`{"s": 1, "e": 2, "c": [123, 45670000000000]}`,      // 123.4567: not an integer
		`{"s": 1, "e": -1, "c": [5]}`,                       // 0.5
		`{"s": 1, "e": 14, "c": [1, 0]}`,                    // trailing zero limbs are dropped
		`{"s": 1, "e": 14, "c": [1, 100000000000000]}`,      // a limb is below 1e14
		`{"s": 1, "e": 14, "c": [1, -5]}`,                   // and not negative
		`{"s": 1, "e": 0, "c": []}`,                         // no coefficient
		`{"s": 1, "e": 0, "c": [5], "x": 1}`,                // another field
		`{"s": "1", "e": 0, "c": [5]}`,                      // a string
		`{"s": 1, "e": 1, "c": [0]}`,                        // zero is c [0], e 0
		`{"s": {"#bigint": "1", "x": 2}, "e": 0, "c": [5]}`, // not a number
		`{"s": {"#set": []}, "e": 0, "c": [5]}`,             // nor a set
	} {
		if n, ok := bigNumber(object(t, src)); ok {
			t.Errorf("%s: read as the integer %s", src, n)
		}
		v, err := fromAny(object(t, src))
		if err != nil || v.Kind != Record {
			t.Errorf("%s: %v, %v; want a record", src, v, err)
		}
	}
}

// digits is the fixture model's record of fields s, e and c.
func digits() *Type {
	return &Type{Kind: Record, Fields: map[string]*Type{"s": {Kind: Int}, "e": {Kind: Int}, "c": {Kind: List, Elem: &Type{Kind: Int}}}}
}

// bigNumberSchema is testdata/bignumber.qnt's types, as binding's typecheck gives them.
func bigNumberSchema() *Schema {
	return &Schema{
		Vars: map[string]*Type{
			"ints":    {Kind: List, Elem: &Type{Kind: Int}},
			"shape":   digits(),
			"both":    {Kind: Tuple, Elems: []*Type{{Kind: Int}, digits()}},
			"byInt":   {Kind: Map, Elem: &Type{Kind: Int}, Value: &Type{Kind: Set, Elem: digits()}},
			"wrapped": {Kind: List, Elem: &Type{Kind: Variant, Fields: map[string]*Type{"Big": {Kind: Int}, "Shape": digits(), "Neither": {Kind: Tuple}}}},
		},
		Picks: map[string]*Type{"n": {Kind: Int}, "d": digits()},
	}
}

func readFixture(t *testing.T, backend string, s *Schema) (*Trace, error) {
	t.Helper()
	b, err := os.ReadFile("testdata/bignumber." + backend + ".itf.json")
	if err != nil {
		t.Fatal(err)
	}
	return ParseWith(b, s)
}

// quint's Rust backend's trace of integers of 16 characters and more
// (bignumber.js objects, in a list, a tuple, a map's key, a variant's
// payload and a pick) is read by the model's types as the TypeScript
// backend's (#bigint) is, the records of fields s, e and c beside them
// read as records; with no types, the first such object fails the read,
// naming where it is and the fix.
func TestRustBigNumbersByType(t *testing.T) {
	rust, err := readFixture(t, "rust", bigNumberSchema())
	if err != nil {
		t.Fatal(err)
	}
	ts, err := readFixture(t, "typescript", bigNumberSchema())
	if err != nil {
		t.Fatal(err)
	}
	if len(rust.States) != 2 || len(ts.States) != 2 {
		t.Fatalf("%d and %d states, want 2", len(rust.States), len(ts.States))
	}
	for i := range rust.States {
		r, s := rust.States[i], ts.States[i]
		for _, name := range []string{"ints", "shape", "both", "byInt", "wrapped"} {
			if !r.Vars[name].Equal(s.Vars[name]) {
				t.Errorf("state %d %s: rust %s, typescript %s", i, name, r.Vars[name], s.Vars[name])
			}
		}
		if fmt.Sprint(r.Picks) != fmt.Sprint(s.Picks) {
			t.Errorf("state %d picks: rust %v, typescript %v", i, r.Picks, s.Picks)
		}
	}
	last := rust.States[1]
	if got := last.Vars["ints"].String(); got != "[0, 999999999999999, 1000000000000000, -99999999999999, -100000000000000, -999999999999999, "+
		"9007199254740991, 9007199254740992, 9007199254740993, 9223372036854775807, -9223372036854775808, 4611686018427387904, "+
		"120000000000000000, 10000000000000001, 1000000000000000000, -9223372036854775808]" {
		t.Errorf("ints: %s", got)
	}
	if got := last.Vars["shape"].String(); got != "{ c: [10], e: 15, s: 1 }" {
		t.Errorf("shape, a record: %s", got)
	}
	if got := last.Picks["n"].String(); got != "-9223372036854775808" {
		t.Errorf("the pick n: %s", got)
	}
	if got := last.Vars["wrapped"].String(); got != "[Big(9223372036854775807), Shape({ c: [92233, 72036854775807], e: 18, s: 1 }), Neither]" {
		t.Errorf("wrapped: %s", got)
	}
	// written back, every integer is ITF's #bigint, read with no types
	out, err := json.Marshal(last.Vars["ints"])
	if err != nil || strings.Contains(string(out), `"e"`) || !strings.Contains(string(out), `{"#bigint":"-9223372036854775808"}`) {
		t.Errorf("ints written back: %s, %v", out, err)
	}

	for _, s := range []*Schema{nil, {}, {Vars: map[string]*Type{"both": {Kind: Tuple, Elems: []*Type{{}, digits()}}}}} {
		_, err := readFixture(t, "rust", s)
		a, ok := errors.AsType[*AmbiguousError](err)
		if !ok || a.State != 0 || a.Var != "both" || a.Path != ".#tup[0]" || a.Int.String() != "-9223372036854775808" ||
			!strings.Contains(err.Error(), "itf.ParseWith") || !strings.Contains(err.Error(), "--backend typescript") {
			t.Errorf("no types (%v): %v", s, err)
		}
	}
	// a record of fields s, e and c is as ambiguous where its type is not known
	_, err = readFixture(t, "typescript", nil)
	if a, ok := errors.AsType[*AmbiguousError](err); !ok || a.Var != "both" || a.Path != ".#tup[1]" {
		t.Errorf("typescript, no types: %v", err)
	}
}
