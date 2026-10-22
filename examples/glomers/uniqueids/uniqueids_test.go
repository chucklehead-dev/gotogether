package uniqueids

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/chucklehead-dev/gotogether/examples/glomers/maelstrom"
	"github.com/chucklehead-dev/gotogether/examples/glomers/maelstrom/hegelnet"
	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"hegel.dev/go/hegel"
)

var nodes = []string{"n1", "n2", "n3"}

func sim(m Mutation, seed uint64) *maelstrom.Sim {
	r := rand.New(rand.NewPCG(seed, 1))
	return maelstrom.NewSim(nodes, func(n *maelstrom.Node) { Setup(n, Options{Mutation: m, Incarnation: r.Uint64}) })
}

// workload: concurrent generate requests in random delivery order, a
// partition, and a restart of n1 half-way.
func workload(s *maelstrom.Sim, seed uint64) {
	r := rand.New(rand.NewPCG(seed, 2))
	pick := func(n int) int { return r.IntN(n) }
	for i := 0; i < 30; i++ {
		s.Request("c1", nodes[r.IntN(len(nodes))], map[string]any{"type": "generate"}, nil)
		if i == 10 {
			s.Partition([]string{"n1"}, []string{"n2", "n3"}) // totally available: ids still flow
		}
		if i == 15 {
			s.Restart("n1")
		}
		if r.IntN(3) == 0 {
			s.Run(pick, 3)
		}
	}
	s.Run(pick, 1000)
}

func check(t *testing.T, binding string, m Mutation) *qtest.Run {
	return qtest.Record(t, binding, func(*qobs.Recorder) { workload(sim(m, 1), 1) })
}

const (
	spec   = "uniqueids.binding.yaml"        // the ids themselves
	scheme = "uniqueids.scheme.binding.yaml" // what they are built from
)

func TestUniqueUnderPartitionAndRestart(t *testing.T) {
	qtest.RequireQuint(t)
	for _, b := range []string{spec, scheme} {
		check(t, b, Faithful).Expect(t, qtest.Conforms())
	}
}

// What each check catches. The spec model sees every repeat, by
// remembering every id. The scheme model only sees a broken scheme (a
// restarted node reusing its incarnation); a broken *encoding*, which
// drops part of (node, incarnation, counter), is invisible to it. That
// part is TestIDEncodingInjective's.
func TestMutants(t *testing.T) {
	qtest.RequireQuint(t)
	for _, c := range []struct {
		m            Mutation
		spec, scheme bool // caught by
	}{
		{CounterOnly, true, false},
		{NoIncarnation, true, false},
		{FixedIncarnation, true, true},
	} {
		for _, b := range []struct {
			file, invariant string
			caught          bool
		}{{spec, "idsUnique", c.spec}, {scheme, "incarnationsOwnedAndCounting", c.scheme}} {
			t.Run(fmt.Sprintf("%d/%s", c.m, b.file), func(t *testing.T) {
				want := qtest.Conforms() // the ids conform, and the invariant holds: not caught
				if b.caught {
					want = qtest.Violates(b.invariant)
				}
				check(t, b.file, c.m).Expect(t, want)
			})
		}
	}
}

// The lemma the scheme model needs: the encoding is injective. A pure
// function, so a property test is the right tool, not the model.
func TestIDEncodingInjective(t *testing.T) {
	gen := func(tc hegel.TestCase) (string, uint64, int) {
		return hegel.Draw(tc, hegel.Text().MaxSize(4)), hegel.Draw(tc, hegel.Integers[uint64](0, 1<<63)), hegel.Draw(tc, hegel.Integers(1, 1<<30))
	}
	hegel.Test(t, func(ht *hegel.T) {
		n, i, c := gen(ht)
		gn, gi, gc, err := ParseID(ID(Faithful, n, i, c))
		if err != nil || gn != n || gi != i || gc != c {
			ht.Fatalf("ParseID(ID(%q, %x, %d)) = %q, %x, %d, %v", n, i, c, gn, gi, gc, err)
		}
	})
	for _, m := range []Mutation{CounterOnly, NoIncarnation} {
		err := hegel.Run(func(tc hegel.TestCase) {
			n1, i1, c1 := hegel.Draw(tc, hegel.SampledFrom(nodes)), hegel.Draw(tc, hegel.Integers[uint64](0, 3)), hegel.Draw(tc, hegel.Integers(1, 3))
			n2, i2, c2 := hegel.Draw(tc, hegel.SampledFrom(nodes)), hegel.Draw(tc, hegel.Integers[uint64](0, 3)), hegel.Draw(tc, hegel.Integers(1, 3))
			if (n1 != n2 || i1 != i2 || c1 != c2) && ID(m, n1, i1, c1) == ID(m, n2, i2, c2) {
				tc.Errorf("(%s, %d, %d) and (%s, %d, %d) both encode as %q", n1, i1, c1, n2, i2, c2, ID(m, n1, i1, c1))
			}
		}, hegel.WithTestCases(200), hegel.WithDatabase(""))
		if err == nil {
			t.Errorf("mutant %d: no collision found", m)
		}
	}
}

// Hegel schedules the network (deliveries, losses, partitions) and the
// requests and restarts; runs are checked in bulk, round after round.
type machine struct {
	hegelnet.Network
}

func (m *machine) RuleGenerate(tc hegel.TestCase) {
	m.Sim.Request("c1", hegel.Draw(tc, hegel.SampledFrom(nodes)), map[string]any{"type": "generate"}, nil)
}

func (m *machine) RuleRestart(tc hegel.TestCase) {
	m.Sim.Restart(hegel.Draw(tc, hegel.SampledFrom(nodes)))
}

func TestHegelSchedules(t *testing.T) {
	qtest.RequireQuint(t)
	qhegel.Test(t, qhegel.Config{Binding: spec, Mode: qhegel.Batched, Opts: []qtest.Option{qtest.Global()}, AllowEmpty: true, Rounds: 2},
		func(ht *hegel.T, _ *qhegel.Case) {
			s := sim(Faithful, uint64(hegel.Draw(ht, hegel.Integers(0, 1<<30))))
			hegel.RunStateful(ht, &machine{Network: hegelnet.Network{Sim: s, Faults: true, NoTimers: true}}, hegel.WithStatefulStepCount(25))
		}, hegel.WithTestCases(30), hegel.WithDatabase(""))
}
