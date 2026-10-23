package qenv

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// DefaultPartitionLength is how long a nemesis's cut lasts at most
// (Network.CutLength).
const DefaultPartitionLength = time.Second

// Partitions. Two processes are partitioned when the network between
// them drops everything: a Service call from one to the other loses its
// request, and a response that would cross the cut is lost; a Queue's
// client cut off from its broker (NewQueue's process) waits, and a consumer's
// messages are redelivered. Unlike the
// per-call faults ("rpc.<name>.request"), a partition lasts: every call
// across it fails until it heals, which is what a node isolated from its
// peers sees, and what a retry loop or a leader election must survive.
//
// Settle heals every partition: the system then gets its chance to
// recover on a whole network.

func pairOf(a, b *Process) [2]string {
	p := [2]string{a.Name, b.Name}
	if p[1] < p[0] {
		p[0], p[1] = p[1], p[0]
	}
	return p
}

// A Network is a simulation's network between its processes (Sim.Net):
// which pairs are cut, and the nemesis that cuts and heals them.
type Network struct {
	// CutLength is how long a cut the nemesis makes (PartitionNemesis)
	// lasts at most (default DefaultPartitionLength); its heal nemesis may
	// end it sooner. Cuts made with Partition last until Heal.
	CutLength time.Duration

	sim   *Sim
	cuts  map[[2]string]bool    // partitioned pairs of processes, by name
	gen   map[[2]string]int     // per pair, the cuts made: a healer heals only its own
	onCut []func(a, b *Process) // OnCut's hooks
}

// Net is the simulation's network.
func (s *Sim) Net() *Network { return &s.net }

// Partition cuts the network between processes a and b.
func (n *Network) Partition(a, b *Process) {
	if n.cuts == nil {
		n.cuts = map[[2]string]bool{}
	}
	if a == b || n.cuts[pairOf(a, b)] {
		return
	}
	n.cuts[pairOf(a, b)] = true
	if n.gen == nil {
		n.gen = map[[2]string]int{}
	}
	n.gen[pairOf(a, b)]++
	for _, h := range n.onCut {
		h(a, b)
	}
}

// Heal restores the network between a and b.
func (n *Network) Heal(a, b *Process) { delete(n.cuts, pairOf(a, b)) }

// healAll restores the whole network (Settle).
func (n *Network) healAll() { n.cuts = nil }

// Partitioned reports whether the network between a and b is cut. A task
// of no process (nil) is never partitioned.
func (n *Network) Partitioned(a, b *Process) bool {
	if a == nil || b == nil || a == b {
		return false
	}
	return n.cuts[pairOf(a, b)]
}

// OnCut registers a hook run when the network between a and b is cut, by
// Partition or the nemesis: a double whose connections a cut breaks (a
// queue's consumers) drops them there.
func (n *Network) OnCut(h func(a, b *Process)) { n.onCut = append(n.onCut, h) }

// PartitionNemesis makes the network between every two of procs a
// nemesis: at any step the scheduler may cut a link
// ("nemesis.partition.<a>.<b>" in a Script) or heal one that is cut
// ("nemesis.heal.<a>.<b>"), at the rate of the partition family
// ("nemesis.partition", heals included). A cut heals by itself after
// CutLength: a network that every task waits on would otherwise never
// heal, since a nemesis acts only at a scheduling step and none comes
// when nothing can run. Settle heals them all.
func (n *Network) PartitionNemesis(procs ...*Process) {
	s := n.sim
	sorted := append([]*Process(nil), procs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for i, a := range sorted {
		for _, b := range sorted[i+1:] {
			s.Nemesis("partition."+a.Name+"."+b.Name, func() bool { return !n.Partitioned(a, b) }, func() {
				n.Partition(a, b)
				gen, d := n.gen[pairOf(a, b)], n.CutLength
				if d == 0 {
					d = DefaultPartitionLength
				}
				s.Spawn(fmt.Sprintf("heal %s-%s", a.Name, b.Name), func(ctx context.Context) {
					Sleep(ctx, d)
					if n.gen[pairOf(a, b)] == gen && n.Partitioned(a, b) {
						n.Heal(a, b)
					}
				})
			})
			s.Nemesis("heal."+a.Name+"."+b.Name, func() bool { return n.Partitioned(a, b) }, func() { n.Heal(a, b) })
		}
	}
}
