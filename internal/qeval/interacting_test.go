package qeval

import (
	"fmt"
	"strconv"
	"testing"
)

// This is an experimental oracle comparison, not a generic incremental
// evaluator. boundedAt is known by construction to read only its own key.
// A production implementation must establish that property itself.
func TestInteractingWrittenEntriesAgreeWithFull(t *testing.T) {
	m := compile(t, "interacting.qnt", "interacting", "init", "step")
	type operation struct {
		name string
		args []Value
	}
	open := func(k string, n int64) operation { return operation{"open", []Value{Str(k), Int(n)}} }
	transfer := func(a, b string, n int64) operation { return operation{"transfer", []Value{Str(a), Str(b), Int(n)}} }
	for _, tc := range []struct {
		name string
		ops  []operation
		bad  int
	}{
		{"passing", []operation{open("a", 10), open("b", 10), transfer("a", "b", 3), transfer("b", "a", 2)}, -1},
		{"destination bound", []operation{open("a", 10), open("b", 19), transfer("a", "b", 2)}, 2},
		{"source bound", []operation{open("a", 1), open("b", 10), transfer("a", "b", 2)}, 2},
		{"new entry", []operation{open("a", 21)}, 0},
		{"repair", []operation{open("a", 10), open("b", 19), transfer("a", "b", 2), transfer("b", "a", 2)}, 2},
		{"unrelated write after failure", []operation{open("a", 21), open("b", 10)}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := m.Init(nil, "init")
			if err != nil {
				t.Fatal(err)
			}
			fp := &Footprint{}
			tracked := m.Tracking(fp)
			initial, err := m.Eval(nil, s, "bounded")
			if err != nil || initial != Bool(true) {
				t.Fatal("initial full oracle", initial, err)
			}
			cached := map[string]bool{}
			firstFull, firstDirty := -1, -1
			for i, op := range tc.ops {
				fp.Reset()
				next, ok, err := tracked.Step(nil, s, op.name, op.args...)
				if err != nil || !ok {
					t.Fatalf("step %d: %v enabled=%v", i, err, ok)
				}
				if fp.Whole != "" {
					t.Fatalf("unexpected unsupported access: %s", fp.Whole)
				}
				// Parse actual footprint keys rather than assuming the source is
				// the only changed account. Transfers must check their destination.
				for _, entry := range fp.Writes {
					if entry.Var != "bal" {
						t.Fatalf("unexpected write %v", entry)
					}
					keyText, err := strconv.Unquote(entry.Key)
					key := Str(keyText)
					if err != nil {
						t.Fatal(err)
					}
					v, err := m.Eval(nil, next, "boundedAt", key)
					if err != nil {
						t.Fatal(err)
					}
					cached[entry.Key] = v == Bool(true)
				}
				incremental := true
				for _, held := range cached {
					incremental = incremental && held
				}
				full, err := m.Eval(nil, next, "bounded")
				if err != nil {
					t.Fatal(err)
				}
				if full != Bool(true) && firstFull < 0 {
					firstFull = i
				}
				if !incremental && firstDirty < 0 {
					firstDirty = i
				}
				if incremental != (full == Bool(true)) {
					t.Fatalf("prefix %d: written-entry=%v full=%v", i, incremental, full)
				}
				s = next
			}
			if firstFull != tc.bad || firstDirty != tc.bad {
				t.Fatalf("first failures full=%d dirty=%d want %d", firstFull, firstDirty, tc.bad)
			}
		})
	}
}

// Two shortcuts are unsound: checking only a transfer's source, and
// applying changed-key checks to a predicate that reads another key.
func TestInteractingInvalidationNegativeControls(t *testing.T) {
	m := compile(t, "interacting.qnt", "interacting", "init", "step")
	eval := func(s State, name string, args ...Value) bool {
		t.Helper()
		v, err := m.Eval(nil, s, name, args...)
		if err != nil {
			t.Fatal(err)
		}
		return v == Bool(true)
	}
	a, b, reserve := Str("a"), Str("b"), Str("reserve")
	s := State{"bal": NewMap([]Value{a, b}, []Value{Int(10), Int(19)})}
	next, ok, err := m.Step(nil, s, "transfer", a, b, Int(2))
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	if !eval(next, "boundedAt", a) || eval(next, "bounded") {
		t.Fatal("source-only control must pass while full bounds fail on destination")
	}
	s = State{"bal": NewMap([]Value{reserve, a, b}, []Value{Int(10), Int(9), Int(0)})}
	if !eval(s, "backed") {
		t.Fatal("control must begin passing")
	}
	fp := &Footprint{}
	next, ok, err = m.Tracking(fp).Step(nil, s, "transfer", reserve, b, Int(2))
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	for _, key := range []Value{reserve, b} {
		if !eval(next, "backedAt", key) {
			t.Fatal("changed-key control must pass")
		}
	}
	if eval(next, "backed") {
		t.Fatal("full predicate must detect unchanged a depends on changed reserve")
	}
	fp = &Footprint{}
	_, ok, err = m.Tracking(fp).Step(nil, s, "reset")
	if err != nil || !ok || fp.Whole == "" {
		t.Fatalf("whole replacement must require fallback: %v %v %q", err, ok, fp.Whole)
	}
}

func interactingState(n int) State {
	keys, vals := make([]Value, n), make([]Value, n)
	for i := range keys {
		keys[i] = Str(fmt.Sprintf("acct-%06d", i))
		vals[i] = Int(10)
	}
	return State{"bal": NewMap(keys, vals)}
}

// Compile and construct input outside timed loops. Splitting transition
// and invariant costs prevents attributing map-copy costs to invariants.
func BenchmarkInteracting(b *testing.B) {
	m := compile(b, "interacting.qnt", "interacting", "init", "step")
	for _, n := range []int{10, 100, 1000} {
		s := interactingState(n)
		src, dest := Str("acct-000000"), Str("acct-000001")
		b.Run(fmt.Sprintf("entities=%d/transition", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, ok, err := m.Step(nil, s, "transfer", src, dest, Int(1))
				if err != nil || !ok {
					b.Fatal(err, ok)
				}
			}
		})
		b.Run(fmt.Sprintf("entities=%d/tracked-transition", n), func(b *testing.B) {
			fp := &Footprint{}
			tracked := m.Tracking(fp)
			b.ReportAllocs()
			for b.Loop() {
				fp.Reset()
				_, ok, err := tracked.Step(nil, s, "transfer", src, dest, Int(1))
				if err != nil || !ok || fp.Whole != "" || len(fp.Writes) != 2 {
					b.Fatal(err, ok, fp)
				}
			}
		})
		b.Run(fmt.Sprintf("entities=%d/invariant", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				v, err := m.Eval(nil, s, "bounded")
				if err != nil || v != Bool(true) {
					b.Fatal(err, v)
				}
			}
		})
		for _, steps := range []int{100, 1000} {
			b.Run(fmt.Sprintf("entities=%d/steps=%d/replay", n, steps), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					state := s
					for i := range steps {
						a, z := src, dest
						if i%2 == 1 {
							a, z = z, a
						}
						next, ok, err := m.Step(nil, state, "transfer", a, z, Int(1))
						if err != nil || !ok {
							b.Fatal(err, ok)
						}
						v, err := m.Eval(nil, next, "bounded")
						if err != nil || v != Bool(true) {
							b.Fatal(err, v)
						}
						state = next
					}
				}
			})
		}
	}
}

// Enumerate every length-three transfer history over boundary balances.
// Full evaluation remains the oracle at every prefix, including repairs.
func TestInteractingWrittenEntriesAgainstFullEnumeration(t *testing.T) {
	m := compile(t, "interacting.qnt", "interacting", "init", "step")
	keys := []Value{Str("a"), Str("b")}
	states := 0
	var walk func(State, map[string]bool, int)
	walk = func(s State, cached map[string]bool, depth int) {
		states++
		full, err := m.Eval(nil, s, "bounded")
		if err != nil {
			t.Fatal(err)
		}
		held := true
		for _, v := range cached {
			held = held && v
		}
		if held != (full == Bool(true)) {
			t.Fatalf("depth %d: cached=%v full=%v state=%v", depth, held, full, s)
		}
		if depth == 3 {
			return
		}
		for _, amount := range []int64{1, 2} {
			for direction := range 2 {
				fp := &Footprint{}
				next, ok, err := m.Tracking(fp).Step(nil, s, "transfer", keys[direction], keys[1-direction], Int(amount))
				if err != nil || !ok {
					t.Fatal(err, ok)
				}
				if fp.Whole != "" {
					t.Fatal(fp.Whole)
				}
				updated := map[string]bool{}
				for k, v := range cached {
					updated[k] = v
				}
				for _, entry := range fp.Writes {
					k, err := strconv.Unquote(entry.Key)
					if err != nil {
						t.Fatal(err)
					}
					v, err := m.Eval(nil, next, "boundedAt", Str(k))
					if err != nil {
						t.Fatal(err)
					}
					updated[entry.Key] = v == Bool(true)
				}
				walk(next, updated, depth+1)
			}
		}
	}
	for _, a := range []int64{0, 1, 19, 20} {
		for _, b := range []int64{0, 1, 19, 20} {
			s := State{"bal": NewMap(keys, []Value{Int(a), Int(b)})}
			walk(s, map[string]bool{`"a"`: true, `"b"`: true}, 0)
		}
	}
	if states != 16*(1+4+16+64) {
		t.Fatalf("enumerated %d prefixes", states)
	}
	t.Logf("%d prefixes agree with full evaluation", states)
}
