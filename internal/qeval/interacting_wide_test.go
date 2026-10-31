package qeval

import (
	"fmt"
	"testing"
)

type ringTransfer struct{ src, dest Value }

func ringWorkload(n, steps int) (map[string]Value, []ringTransfer) {
	initial := interactingState(n)["bal"].(*Map)
	ops := make([]ringTransfer, steps)
	for i := range ops {
		ops[i] = ringTransfer{initial.Keys[i%n], initial.Keys[(i+1)%n]}
	}
	return map[string]Value{"INITIAL": initial, "TOTAL": Int(10 * n)}, ops
}

// The trace has a known independent balance oracle, visits every ring edge,
// and checks the global property after every prefix rather than at the end.
func TestInteractingWideRing(t *testing.T) {
	m := compile(t, "interacting_wide.qnt", "interacting_wide", "init", "step")
	for _, n := range []int{3, 10, 100} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			consts, ops := ringWorkload(n, 2*n)
			s, err := m.Init(consts, "init")
			if err != nil {
				t.Fatal(err)
			}
			balances := make([]int64, n)
			for i := range balances {
				balances[i] = 10
			}
			states := []State{s}
			expected := [][]int64{append([]int64(nil), balances...)}
			edges, accounts := map[[2]string]bool{}, map[string]bool{}
			for i, op := range ops {
				next, ok, err := m.Step(consts, s, "transfer", op.src, op.dest, Int(1))
				if err != nil || !ok {
					t.Fatalf("prefix %d: enabled=%v error=%v", i+1, ok, err)
				}
				balances[i%n]--
				balances[(i+1)%n]++
				actual := next["bal"].(*Map)
				var total int64
				for k, want := range balances {
					total += want
					if actual.Vals[k] != Int(want) {
						t.Fatalf("prefix %d account %d: got %v want %d", i+1, k, actual.Vals[k], want)
					}
				}
				if total != int64(10*n) {
					t.Fatalf("oracle total at prefix %d: %d", i+1, total)
				}
				for _, property := range []string{"bounded", "conserved"} {
					v, err := m.Eval(consts, next, property)
					if err != nil || v != Bool(true) {
						t.Fatalf("%s at prefix %d: %v (%v)", property, i+1, v, err)
					}
				}
				a, z := String(op.src), String(op.dest)
				edges[[2]string{a, z}], accounts[a], accounts[z] = true, true, true
				if (i+1)%n == 0 {
					if len(edges) != n || len(accounts) != n {
						t.Fatalf("cycle: reached %d edges/%d accounts, want %d each", len(edges), len(accounts), n)
					}
					if Compare(actual, consts["INITIAL"]) != 0 {
						t.Fatalf("cycle ending at prefix %d did not restore initial balances", i+1)
					}
					edges, accounts = map[[2]string]bool{}, map[string]bool{}
				}
				states = append(states, next)
				expected = append(expected, append([]int64(nil), balances...))
				s = next
			}
			// Retained prefix states must survive later writes, even though
			// every full cycle returns to the same final balances.
			for prefix, state := range states {
				actual := state["bal"].(*Map)
				for account, want := range expected[prefix] {
					if actual.Vals[account] != Int(want) {
						t.Fatalf("retained prefix %d account %d mutated: got %v want %d", prefix, account, actual.Vals[account], want)
					}
				}
			}
		})
	}
}

func TestInteractingWideDestinationContrast(t *testing.T) {
	m := compile(t, "interacting_wide.qnt", "interacting_wide", "init", "step")
	const n, badPrefix = 10, 7
	consts, ops := ringWorkload(n, 2*n)
	s, err := m.Init(consts, "init")
	if err != nil {
		t.Fatal(err)
	}
	firstFailure := 0
	for i, op := range ops {
		action := "transfer"
		if i+1 == badPrefix {
			action = "extraCredit"
		}
		next, ok, err := m.Step(consts, s, action, op.src, op.dest, Int(1))
		if err != nil || !ok {
			t.Fatalf("prefix %d: enabled=%v error=%v", i+1, ok, err)
		}
		bounded, err := m.Eval(consts, next, "bounded")
		if err != nil || bounded != Bool(true) {
			t.Fatalf("contrast must remain bounded at prefix %d: %v (%v)", i+1, bounded, err)
		}
		conserved, err := m.Eval(consts, next, "conserved")
		if err != nil {
			t.Fatal(err)
		}
		if conserved != Bool(i+1 < badPrefix) {
			t.Fatalf("conservation at prefix %d: %v", i+1, conserved)
		}
		if conserved == Bool(false) && firstFailure == 0 {
			firstFailure = i + 1
		}
		s = next
	}
	if firstFailure != badPrefix {
		t.Fatalf("first conservation failure %d, want %d", firstFailure, badPrefix)
	}
}

// Input construction and compilation are outside timed loops. A complete ring
// visits all accounts; shorter traces explicitly measure only their prefix.
func BenchmarkInteractingWide(b *testing.B) {
	m := compile(b, "interacting_wide.qnt", "interacting_wide", "init", "step")
	for _, n := range []int{10, 100, 1000} {
		consts, cycle := ringWorkload(n, n)
		s, err := m.Init(consts, "init")
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("entities=%d/transition", n), func(b *testing.B) {
			i := 0
			b.ReportAllocs()
			for b.Loop() {
				op := cycle[i]
				_, ok, err := m.Step(consts, s, "transfer", op.src, op.dest, Int(1))
				if err != nil || !ok {
					b.Fatal(err, ok)
				}
				i = (i + 1) % n
			}
		})
		for _, property := range []string{"bounded", "conserved"} {
			b.Run(fmt.Sprintf("entities=%d/%s", n, property), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					v, err := m.Eval(consts, s, property)
					if err != nil || v != Bool(true) {
						b.Fatal(err, v)
					}
				}
			})
		}
		for _, steps := range []int{100, 1000} {
			_, ops := ringWorkload(n, steps)
			b.Run(fmt.Sprintf("entities=%d/transfers=%d/replay", n, steps), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					state := s
					for _, op := range ops {
						next, ok, err := m.Step(consts, state, "transfer", op.src, op.dest, Int(1))
						if err != nil || !ok {
							b.Fatal(err, ok)
						}
						for _, property := range []string{"bounded", "conserved"} {
							v, err := m.Eval(consts, next, property)
							if err != nil || v != Bool(true) {
								b.Fatal(err, v)
							}
						}
						state = next
					}
				}
			})
		}
	}
}
