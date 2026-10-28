package qeval

import (
	"sync"
	"testing"
)

// The explorer's costs (I39): a bounded explore of three models (bank's
// step of three nondets and three branches, kvstore's records in a map,
// payd's eight variables of maps and sets), one step as a replay takes
// it, one state's transitions, and a state's key.

// benchModels are the models the benchmarks explore, each with its
// constants and its step's state bound.
func benchModels(b *testing.B) []benchModel {
	b.Helper()
	benchOnce.Do(func() { benchCache = compileBench(b) })
	if benchCache == nil {
		b.Skip("the models did not compile")
	}
	return benchCache
}

// a benchmark's model: its constants and the explore's state bound
type benchModel struct {
	name   string
	m      *Model
	consts map[string]Value
	states int
}

// benchCache holds the compiled models, process-wide for the test
// binary's benchmarks (each compile is a quint process)
var (
	benchOnce  sync.Once
	benchCache []benchModel
)

func compileBench(b *testing.B) []benchModel {
	bank := compileFile(b, "../../connect/testdata/bank.qnt", "bank")
	kv := compileFile(b, "../../examples/kvstore/model/kv.qnt", "kv")
	payd := compileFile(b, "../../examples/payd/model/payd.qnt", "payd")
	return []benchModel{
		{"bank", bank, map[string]Value{}, 2000},
		{"kvstore", kv, map[string]Value{"KEYS": strs("a", "b"), "VALUES": strs("x", "y", "z")}, 1000},
		{"payd", payd, map[string]Value{"INITIAL": NewMap([]Value{Str("acct_1"), Str("acct_2")}, []Value{Int(1000), Int(500)})}, 1000},
	}
}

func BenchmarkExplore(b *testing.B) {
	for _, c := range benchModels(b) {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				g, err := c.m.Explore(c.consts, "init", "step", Bound{States: c.states}, nil)
				if err != nil {
					b.Fatal(err)
				}
				if len(g.States) != c.states {
					b.Fatalf("%d states, not %d", len(g.States), c.states)
				}
			}
		})
	}
}

// a state some steps from init, by the first transition of each
func deepState(b *testing.B, m *Model, consts map[string]Value, n int) State {
	b.Helper()
	s, err := m.Init(consts, "init")
	if err != nil {
		b.Fatal(err)
	}
	for range n {
		ts, err := m.Transitions(consts, s, "step")
		if err != nil || len(ts) == 0 {
			b.Fatal(err, len(ts))
		}
		s = ts[len(ts)/2].State
	}
	return s
}

func BenchmarkStep(b *testing.B) {
	ms := benchModels(b)
	bank, kv := ms[0], ms[1]
	s := deepState(b, bank.m, bank.consts, 4)
	b.Run("bank/deposit", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, ok, err := bank.m.Step(bank.consts, s, "deposit", Str("alice"), Int(3)); err != nil || !ok {
				b.Fatal(err, ok)
			}
		}
	})
	ks := deepState(b, kv.m, kv.consts, 4)
	b.Run("kvstore/write", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, ok, err := kv.m.Step(kv.consts, ks, "write", Str("a"), Str("y")); err != nil || !ok {
				b.Fatal(err, ok)
			}
		}
	})
	for _, c := range ms {
		s := deepState(b, c.m, c.consts, 4)
		b.Run(c.name+"/transitions", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := c.m.Transitions(c.consts, s, "step"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkKey(b *testing.B) {
	for _, c := range benchModels(b) {
		s := deepState(b, c.m, c.consts, 6)
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = c.m.Key(s)
			}
		})
	}
}
