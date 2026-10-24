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

// A Network is a simulation's network between its processes: which pairs
// are cut, and the nemesis that cuts and heals them. Obtain it with
// Sim.Net; a Network of no simulation does nothing useful.
type Network struct {
	// CutLength is how long a cut the nemesis makes (PartitionNemesis)
	// lasts at most (default DefaultPartitionLength); its heal nemesis may
	// end it sooner. Cuts made with Partition last until Heal.
	CutLength time.Duration

	sim   *Sim
	cuts  map[[2]string]bool    // partitioned pairs of processes, by name
	hand  map[[2]string]bool    // the cuts Partition made, which only Heal ends
	gen   map[[2]string]int     // per pair, the cuts made: a healer heals only its own
	onCut []func(a, b *Process) // OnCut's hooks
}

// Net is the simulation's network.
func (s *Sim) Net() *Network { return &s.net }

// Partition cuts the network between processes a and b.
func (n *Network) Partition(a, b *Process) {
	if a == b {
		return
	}
	if n.hand == nil {
		n.hand = map[[2]string]bool{}
	}
	n.hand[pairOf(a, b)] = true // over a cut of the nemesis's too: neither of its healers ends it now
	n.cut(a, b)
}

// cut cuts the network between a and b, by hand or by the nemesis.
func (n *Network) cut(a, b *Process) {
	if n.cuts == nil {
		n.cuts = map[[2]string]bool{}
	}
	if n.gen == nil {
		n.gen = map[[2]string]int{}
	}
	n.gen[pairOf(a, b)]++ // a cut made over the nemesis's: its timed healer's generation is past
	if n.cuts[pairOf(a, b)] {
		return
	}
	n.cuts[pairOf(a, b)] = true
	for _, h := range n.onCut {
		h(a, b)
	}
}

// Heal restores the network between a and b.
func (n *Network) Heal(a, b *Process) {
	delete(n.cuts, pairOf(a, b))
	delete(n.hand, pairOf(a, b))
}

// healAll restores the whole network (Settle).
func (n *Network) healAll() { n.cuts, n.hand = nil, nil }

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
// when nothing can run. Settle heals them all. A pair that is a nemesis
// already (GOTOGETHER_ALL_FAULTS makes every pair one) stays one.
func (n *Network) PartitionNemesis(procs ...*Process) {
	s := n.sim
	sorted := append([]*Process(nil), procs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for i, a := range sorted {
		for _, b := range sorted[i+1:] {
			if s.hasNemesis("partition." + a.Name + "." + b.Name) {
				continue
			}
			s.nemesisOf("partition."+a.Name+"."+b.Name, []*Process{a, b}, func() bool { return !n.Partitioned(a, b) }, func() {
				n.cut(a, b)
				gen, d := n.gen[pairOf(a, b)], n.CutLength
				if d == 0 {
					d = DefaultPartitionLength
				}
				s.Spawn(fmt.Sprintf("heal %s-%s", a.Name, b.Name), func(ctx context.Context) {
					// a daemon: Settle heals every cut, and a heal still
					// sleeping is no work left (it kept a run from settling)
					Daemon(ctx)
					Sleep(ctx, d)
					if n.gen[pairOf(a, b)] == gen && n.Partitioned(a, b) {
						n.Heal(a, b)
					}
				})
			})
			// the nemesis heals its own cuts only: one made with Partition
			// lasts until Heal
			s.nemesisOf("heal."+a.Name+"."+b.Name, []*Process{a, b}, func() bool { return n.Partitioned(a, b) && !n.hand[pairOf(a, b)] }, func() { n.Heal(a, b) })
		}
	}
}

// hasNemesis reports whether a nemesis of that name is registered.
func (s *Sim) hasNemesis(name string) bool {
	for _, nm := range s.nemeses {
		if nm.name == name {
			return true
		}
	}
	return false
}
