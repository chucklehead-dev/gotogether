package binding

import (
	"fmt"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
)

// A model's Schema reads quint's Rust backend's trace of integers of 16
// characters and more (bignumber.js objects: itf's
// testdata/bignumber.qnt, beside records of the fields s, e and c, in
// aliases, a tuple, a map, a sum and picks) as the TypeScript backend's
// (#bigint) reads; a Model made by hand has none, and refuses it.
func TestSchemaReadsRustIntegers(t *testing.T) {
	skip.Quint(t)
	m, err := LoadModel("quint", "../itf/testdata/bignumber.qnt", "bignumber")
	if err != nil {
		t.Fatal(err)
	}
	s := m.Schema()
	for _, v := range []string{"ints", "shape", "both", "byInt", "wrapped"} {
		if s.Vars[v] == nil {
			t.Errorf("no type for the variable %s", v)
		}
	}
	if s.Picks["n"] == nil || s.Picks["n"].Kind != itf.Int || s.Picks["d"] == nil || s.Picks["d"].Kind != itf.Record {
		t.Errorf("picks' types: %v", s.Picks)
	}
	rust, err := itf.ReadFileWith("../itf/testdata/bignumber.rust.itf.json", s)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := itf.ReadFileWith("../itf/testdata/bignumber.typescript.itf.json", s)
	if err != nil {
		t.Fatal(err)
	}
	for i := range rust.States {
		if a, b := fmt.Sprint(rust.States[i].Vars, rust.States[i].Picks), fmt.Sprint(ts.States[i].Vars, ts.States[i].Picks); a != b {
			t.Errorf("state %d: rust %s\ntypescript %s", i, a, b)
		}
	}
	if _, err := itf.ReadFileWith("../itf/testdata/bignumber.rust.itf.json", (&Model{}).Schema()); err == nil {
		t.Error("a Model made by hand read the Rust trace")
	}
}
