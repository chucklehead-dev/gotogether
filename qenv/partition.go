package qenv

import "sort"

// Partitions. Two processes are partitioned when the network between
// them drops everything: a Service call from one to the other loses its
// request, and a response that would cross the cut is lost. Unlike the
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

// Partition cuts the network between processes a and b.
func (s *Sim) Partition(a, b *Process) {
	if s.cuts == nil {
		s.cuts = map[[2]string]bool{}
	}
	s.cuts[pairOf(a, b)] = true
}

// Heal restores the network between a and b.
func (s *Sim) Heal(a, b *Process) { delete(s.cuts, pairOf(a, b)) }

// Partitioned reports whether the network between a and b is cut. A task
// of no process (nil) is never partitioned.
func (s *Sim) Partitioned(a, b *Process) bool {
	if a == nil || b == nil || a == b {
		return false
	}
	return s.cuts[pairOf(a, b)]
}

// Partitions makes the network between every two of procs a nemesis: at
// any step the scheduler may cut a link ("nemesis.partition.<a>.<b>" in a
// Script) or heal one that is cut ("nemesis.heal.<a>.<b>"), at the
// nemesis rate. Settle heals them all.
func (s *Sim) Partitions(procs ...*Process) {
	sorted := append([]*Process(nil), procs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for i, a := range sorted {
		for _, b := range sorted[i+1:] {
			s.Nemesis("partition."+a.Name+"."+b.Name, func() bool { return !s.Partitioned(a, b) }, func() { s.Partition(a, b) })
			s.Nemesis("heal."+a.Name+"."+b.Name, func() bool { return s.Partitioned(a, b) }, func() { s.Heal(a, b) })
		}
	}
}
