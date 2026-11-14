package qeval

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/itf"
)

// ToITF is FromITF's inverse on every kind a state holds.
func TestToITFRoundTrips(t *testing.T) {
	for _, lit := range []string{
		`5`, `-3`, `true`, `"a"`, `[1, 2]`, `(1, "x")`, `Set(3, 1, 2)`,
		`Map("a" -> 1, "b" -> 2)`, `{ x: 1, y: Set("p") }`, `Some(4)`, `None`,
		`Map(1 -> { q: [Set()], r: (true, Set(Map())) })`,
	} {
		v, err := itf.ParseQuint(lit)
		if err != nil {
			t.Fatalf("%s: %v", lit, err)
		}
		q, err := FromITF(v)
		if err != nil {
			t.Fatalf("%s: %v", lit, err)
		}
		back, err := ToITF(q)
		if err != nil {
			t.Fatalf("%s: %v", lit, err)
		}
		again, err := FromITF(back)
		if err != nil {
			t.Fatalf("%s: %v", lit, err)
		}
		if Compare(q, again) != 0 {
			t.Errorf("%s: %s came back as %s", lit, String(q), String(again))
		}
		if !back.Equal(v) {
			t.Errorf("%s: ITF %v came back as %v", lit, v, back)
		}
	}
	if _, err := ToITF(&Lambda{}); err == nil {
		t.Error("an operator has an ITF form")
	}
}
