package qeval

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"testing"
)

// decodeKey is the inverse of appendKey: the value b's first encoding
// gives, and what follows it. That it exists is the argument that keys
// are collision-free: a value's encoding determines the value (Compare
// equal), and where it ends, so a state's key, its variables' encodings
// one after another, determines every variable.
func decodeKey(t *testing.T, b []byte) (Value, []byte) {
	t.Helper()
	if len(b) == 0 {
		t.Fatal("a key ends inside a value")
	}
	tag, b := b[0], b[1:]
	uv := func() int {
		n, w := binary.Uvarint(b)
		if w <= 0 {
			t.Fatal("a bad count")
		}
		b = b[w:]
		return int(n)
	}
	str := func() string {
		n := uv()
		s := string(b[:n])
		b = b[n:]
		return s
	}
	seq := func() []Value {
		out := make([]Value, uv())
		for i := range out {
			out[i], b = decodeKey(t, b)
		}
		return out
	}
	switch tag {
	case tagInt:
		n, w := binary.Varint(b)
		if w <= 0 {
			t.Fatal("a bad integer")
		}
		return Int(n), b[w:]
	case tagFalse:
		return Bool(false), b
	case tagTrue:
		return Bool(true), b
	case tagStr:
		return Str(str()), b
	case tagSet:
		return &Set{seq()}, b
	case tagMap:
		m := &Map{}
		for range uv() {
			var k, v Value
			k, b = decodeKey(t, b)
			v, b = decodeKey(t, b)
			m.Keys, m.Vals = append(m.Keys, k), append(m.Vals, v)
		}
		return m, b
	case tagRec:
		r := &Rec{}
		for range uv() {
			n := str()
			var v Value
			v, b = decodeKey(t, b)
			r.Names, r.Vals = append(r.Names, n), append(r.Vals, v)
		}
		return r, b
	case tagTup:
		return Tup(seq()), b
	case tagList:
		return List(seq()), b
	case tagVariant:
		l := str()
		var v Value
		v, b = decodeKey(t, b)
		return Variant{l, v}, b
	case tagInfinite:
		nat := b[0] == 1
		return infinite{nat}, b[1:]
	}
	t.Fatalf("no value has the tag %d", tag)
	return nil, nil
}

// randValue is a random value of depth at most d, over small alphabets
// so that equal values and near misses (Tup("ab", "c") and Tup("a",
// "bc"), a Tup and a List of the same elements, a string holding a
// tag's byte) come up often.
func randValue(r *rand.Rand, d int) Value {
	strs := []string{"", "a", "b", "ab", "c", "bc", ";", "\x00", "\xf1", "\xf3a", `"`, "a, b"}
	k := r.IntN(12)
	if d <= 0 {
		k = r.IntN(4)
	}
	elems := func() []Value {
		out := make([]Value, r.IntN(4))
		for i := range out {
			out[i] = randValue(r, d-1)
		}
		return out
	}
	switch k {
	case 0:
		return Int([]int64{0, 1, -1, 63, 64, -64, 1 << 40, -1 << 63, 1<<63 - 1}[r.IntN(9)])
	case 1:
		return Bool(r.IntN(2) == 0)
	case 2:
		return Str(strs[r.IntN(len(strs))])
	case 3:
		return infinite{r.IntN(2) == 0}
	case 4, 5:
		return NewSet(elems())
	case 6:
		ks := elems()
		vs := make([]Value, len(ks))
		for i := range vs {
			vs[i] = randValue(r, d-1)
		}
		return NewMap(ks, vs)
	case 7:
		vs := elems()
		names := make([]string, len(vs))
		for i := range names {
			names[i] = fmt.Sprintf("f%s%d", strs[r.IntN(len(strs))], i)
		}
		return NewRec(names, vs)
	case 8:
		return Tup(elems())
	case 9:
		return List(elems())
	case 10:
		return Variant{strs[r.IntN(len(strs))], randValue(r, d-1)}
	}
	return NewSet(nil)
}

// Keys are collision-free: every state's key decodes back to the state
// (each variable Compare-equal, the key consumed exactly), and among
// states drawn so that many coincide, two keys are equal exactly when
// the states are. A key without a string's length (strings run
// together) fails the first, one with a Tup and a List under one tag
// both.
func TestKeyCollisionFree(t *testing.T) {
	m := &Model{Vars: []string{"a", "b", "c"}}
	r := rand.New(rand.NewPCG(1, 39))
	var states []State
	for range 3000 {
		s := State{}
		for _, v := range m.Vars {
			if r.IntN(10) > 0 { // sometimes absent
				s[v] = randValue(r, 3)
			}
		}
		states = append(states, s)
		b := []byte(m.Key(s))
		for _, v := range m.Vars {
			if len(b) > 0 && b[0] == tagAbsent {
				if _, ok := s[v]; ok {
					t.Fatalf("%s: absent in the key of %v", v, showState(s))
				}
				b = b[1:]
				continue
			}
			var x Value
			x, b = decodeKey(t, b)
			if Compare(x, s[v]) != 0 {
				t.Fatalf("%s: the key decodes to %s, not %s", v, String(x), String(s[v]))
			}
		}
		if len(b) != 0 {
			t.Fatalf("%d bytes past the last variable", len(b))
		}
	}
	same := func(x, y State) bool {
		for _, v := range m.Vars {
			a, aok := x[v]
			b, bok := y[v]
			if aok != bok || aok && Compare(a, b) != 0 {
				return false
			}
		}
		return true
	}
	// small states, so that equal ones are frequent
	var small []State
	for range 4000 {
		s := State{}
		for _, v := range m.Vars {
			s[v] = randValue(r, 1)
		}
		small = append(small, s)
	}
	equal := 0
	for _, set := range [][]State{states, small} {
		keys := map[string]int{}
		for i, s := range set {
			k := m.Key(s)
			if j, ok := keys[k]; ok {
				if !same(set[j], s) {
					t.Fatalf("one key for two states: %s and %s", showState(set[j]), showState(s))
				}
				equal++
				continue
			}
			keys[k] = i
		}
		for i := 0; i+1 < len(set); i++ {
			if same(set[i], set[i+1]) != (m.Key(set[i]) == m.Key(set[i+1])) {
				t.Fatalf("keys disagree with Compare: %s and %s", showState(set[i]), showState(set[i+1]))
			}
		}
	}
	if equal < 50 {
		t.Fatalf("only %d states coincided: the test draws too few equal states to mean anything", equal)
	}
}
