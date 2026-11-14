package qeval

import (
	"math"
	"slices"
	"strings"
	"testing"
)

// Every retained state/edge must be real, and every claimed complete prefix
// must contain the full graph's states through that depth. Exercise budgets
// inside node, label and edge admissions, including a cut among initial states.
func TestGraphMemoryBoundAgainstFullGraph(t *testing.T) {
	m := compile(t, "../../../validate/testdata/graph.qnt", "graph", "init", "step")
	if _, err := m.Reach(nil, State{}, "step", nil, Bound{MemoryBytes: 1}); err == nil {
		t.Fatal("Reach silently ignored a graph-only memory budget")
	}
	full, err := m.Explore(nil, "init", "step", Bound{}, nil)
	if err != nil || !full.Complete || full.MemoryUsed <= 0 {
		t.Fatalf("reference: %v %+v", err, full)
	}
	least := map[string]int{}
	for i, st := range full.States {
		least[m.Key(st)] = full.Depth[i]
	}
	dead := map[string]bool{}
	for _, i := range full.Deadlocks() {
		dead[m.Key(full.States[i])] = true
	}
	cuts, initial, partial := 0, 0, 0
	budgets := []int64{1, full.MemoryUsed - 1, full.MemoryUsed, full.MemoryUsed + 1}
	for n := int64(128); n < full.MemoryUsed; n += 128 {
		budgets = append(budgets, n)
	}
	for _, n := range budgets {
		g, err := m.Explore(nil, "init", "step", Bound{MemoryBytes: n}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if g.MemoryUsed > n {
			t.Fatalf("budget=%d retained estimate=%d", n, g.MemoryUsed)
		}
		if g.Complete {
			if g.Cut != "" || len(g.States) != len(full.States) || g.Edges() != full.Edges() {
				t.Fatalf("budget=%d false completion", n)
			}
			continue
		}
		cuts++
		if g.Cut != "memory" {
			t.Fatalf("budget=%d cut=%s", n, g.Cut)
		}
		if g.CompleteDepth < 0 {
			initial++
		}
		have := map[string]bool{}
		for _, st := range g.States {
			k := m.Key(st)
			have[k] = true
			if _, ok := least[k]; !ok {
				t.Fatal("invented state")
			}
		}
		for k, d := range least {
			if d <= g.CompleteDepth && !have[k] {
				t.Fatalf("budget=%d missing state at claimed complete depth%d", n, d)
			}
		}
		for i, out := range g.Out {
			if !g.Expanded[i] && len(out) > 0 {
				partial++
			}
			for _, e := range out {
				if e.To < 0 || e.To >= len(g.States) || e.Label < 0 || e.Label >= len(g.Labels) {
					t.Fatalf("dangling edge: %+v", e)
				}
				from, _ := full.ID(g.States[i])
				to, _ := full.ID(g.States[e.To])
				found := false
				for _, orig := range full.Out[from] {
					if orig.To == to && slices.EqualFunc(full.Labels[orig.Label], g.Labels[e.Label], func(a, b Event) bool { return a.String() == b.String() }) {
						found = true
						break
					}
				}
				if !found {
					t.Fatal("invented edge")
				}
			}
		}
		for _, i := range g.Deadlocks() {
			if !dead[m.Key(g.States[i])] {
				t.Fatal("partial expansion presented as deadlock")
			}
		}
	}
	if cuts == 0 || initial == 0 || partial == 0 {
		t.Fatalf("missing cutoff coverage: cuts=%d initial=%d partial=%d", cuts, initial, partial)
	}
	exact, err := m.Explore(nil, "init", "step", Bound{MemoryBytes: full.MemoryUsed}, nil)
	if err != nil || !exact.Complete {
		t.Fatalf("exact sufficient budget: %v %+v", err, exact)
	}
}

func TestGraphMemoryAccountingIncludesPayloadAndSaturates(t *testing.T) {
	small := stateMemory(State{"v": List{Str("a")}}, "key")
	large := stateMemory(State{"v": List{Str(strings.Repeat("x", 4096))}}, "key")
	if large-small < 4095 {
		t.Fatal("state payload omitted from storage estimate")
	}
	a := labelMemory([]Event{{Action: "a", Args: []Value{Str("a")}}}, nil)
	b := labelMemory([]Event{{Action: "a", Args: []Value{Str(strings.Repeat("x", 4096))}}}, nil)
	if b-a < 4095 {
		t.Fatal("label payload omitted from storage estimate")
	}
	if memorySum(math.MaxInt64-1, 2) != math.MaxInt64 {
		t.Fatal("storage accounting overflowed")
	}
}

// Report the deterministic storage estimate separately from Go's allocation
// metrics (which include temporary evaluation work, not just retained data).
func BenchmarkGraphStorageEstimate(b *testing.B) {
	for _, c := range benchModels(b) {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			var used int64
			var states int
			for b.Loop() {
				g, err := c.m.Explore(c.consts, "init", "step", Bound{States: c.states, MemoryBytes: 256 << 20}, nil)
				if err != nil {
					b.Fatal(err)
				}
				used, states = g.MemoryUsed, len(g.States)
			}
			if states > 0 {
				b.ReportMetric(float64(used)/float64(states), "estimated-B/state")
			}
		})
	}
}
