package qeval

import (
	"slices"
	"testing"
)

type mapReferenceEntry struct{ key, value Value }

// A reference update deliberately uses an unsorted entry list and linear key
// lookup, independently of Map's binary search and parallel sorted slices.
func referenceMapPut(before []mapReferenceEntry, key, value Value) []mapReferenceEntry {
	after := slices.Clone(before)
	for i := range after {
		if Compare(after[i].key, key) == 0 {
			after[i].value = value
			return after
		}
	}
	return append(after, mapReferenceEntry{key, value})
}

func checkMapReference(t *testing.T, got *Map, want []mapReferenceEntry) {
	t.Helper()
	if len(got.Keys) != len(want) || len(got.Vals) != len(want) {
		t.Fatalf("map has %d keys/%d values, want %d: %s", len(got.Keys), len(got.Vals), len(want), String(got))
	}
	for i, key := range got.Keys {
		if i > 0 && Compare(got.Keys[i-1], key) >= 0 {
			t.Fatalf("map keys are not strictly ordered: %s", String(got))
		}
		found := false
		for _, entry := range want {
			if Compare(entry.key, key) == 0 {
				found = true
				if Compare(entry.value, got.Vals[i]) != 0 {
					t.Fatalf("key %s has %s, want %s", String(key), String(got.Vals[i]), String(entry.value))
				}
				break
			}
		}
		if !found {
			t.Fatalf("unexpected key %s in %s", String(key), String(got))
		}
	}
}

// Updating an existing key and later inserting must preserve the source and
// sibling states, including when the source's backing storage has spare room.
func TestMapPutPreservesSnapshotsWithSpareCapacity(t *testing.T) {
	keys, values := make([]Value, 2, 8), make([]Value, 2, 8)
	keys[0], keys[1] = Str("b"), Str("d")
	values[0], values[1] = Int(1), Int(2)
	base := &Map{Keys: keys, Vals: values}
	wantBase := []mapReferenceEntry{{Str("b"), Int(1)}, {Str("d"), Int(2)}}
	left := base.put(Str("b"), Int(10))
	wantLeft := referenceMapPut(wantBase, Str("b"), Int(10))
	right := base.put(Str("d"), Int(20))
	wantRight := referenceMapPut(wantBase, Str("d"), Int(20))
	insertedLeft := left.put(Str("c"), Int(30))
	insertedRight := right.put(Str("a"), Int(40))
	updatedAgain := insertedLeft.put(Str("d"), Int(50))
	checkMapReference(t, base, wantBase)
	checkMapReference(t, left, wantLeft)
	checkMapReference(t, right, wantRight)
	checkMapReference(t, insertedLeft, referenceMapPut(wantLeft, Str("c"), Int(30)))
	checkMapReference(t, insertedRight, referenceMapPut(wantRight, Str("a"), Int(40)))
	checkMapReference(t, updatedAgain, referenceMapPut(referenceMapPut(wantLeft, Str("c"), Int(30)), Str("d"), Int(50)))
}

func TestMapPutAgainstCopyingReference(t *testing.T) {
	// Equal-but-distinct record keys exercise semantic equality rather than
	// pointer identity. Numeric and string keys exercise ordering across kinds.
	keys := []Value{Int(-1), Int(2), Str("a"), NewRec([]string{"x"}, []Value{Int(1)}), NewRec([]string{"x"}, []Value{Int(1)})}
	type snapshot struct {
		value *Map
		want  []mapReferenceEntry
	}
	prefixes := 0
	var walk func([]snapshot, int)
	walk = func(history []snapshot, depth int) {
		prefixes++
		for _, old := range history {
			checkMapReference(t, old.value, old.want)
		}
		if depth == 3 {
			return
		}
		before := history[len(history)-1]
		for _, key := range keys {
			for _, value := range []Value{Int(0), Int(9)} {
				after := snapshot{before.value.put(key, value), referenceMapPut(before.want, key, value)}
				walk(append(slices.Clone(history), after), depth+1)
			}
		}
	}
	walk([]snapshot{{&Map{}, nil}}, 0)
	if prefixes != 1+10+100+1000 {
		t.Fatalf("checked %d prefixes, want 1111", prefixes)
	}
	t.Logf("%d bounded prefixes preserve every retained map", prefixes)
}
