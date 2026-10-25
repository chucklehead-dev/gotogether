// Raft's leadership as the nodes record it: a node campaigns for a term,
// leads it, steps down from it, or stops (crashes); the test cuts a node
// off and heals the network; a node records where its state machine is
// (boot, apply). Three properties:
//
//   - electionSafety: at most one node leads a term, ever (raft's
//     Election Safety), kept by a ghost that remembers every term led.
//   - after the network heals, some node leads the newest term and is not
//     cut off (hasLeader), within a bound the binding gives.
//   - a node that restarts behind the others (its applied index below the
//     highest any other applied when it booted) catches up to it
//     (caughtUp), within a bound the binding gives: by the leader's log,
//     or by a snapshot once the log is compacted.
//
// Terms come from the recorded campaigns: a node leads a term it
// campaigned for, and the newest term is the newest campaign's.
module cluster {
  const NODES: Set[str]

  var cut: Set[str]
  var everCut: bool
  var maxTerm: int
  var leader: str -> int
  // ghost: every (node, term) led, and whether two nodes led one term
  var led: Set[(str, int)]
  var twoLeaders: bool
  // each node's applied index, and the index a node that booted behind
  // must reach (none: not behind)
  var applied: str -> int
  var target: str -> int

  action init = all {
    cut' = Set(), everCut' = false, maxTerm' = 1, leader' = Map(), led' = Set(), twoLeaders' = false,
    applied' = NODES.mapBy(_ => 1), target' = Map(),
  }

  action unchangedLeadership = all { maxTerm' = maxTerm, leader' = leader, led' = led, twoLeaders' = twoLeaders, applied' = applied, target' = target }

  action unchangedLogs = all { applied' = applied, target' = target }

  // the highest index a node other than node applied
  def othersApplied(node: str): int = NODES.exclude(Set(node)).fold(0, (m, o) => if (applied.get(o) > m) applied.get(o) else m)

  action isolate(node: str): bool = all {
    NODES.contains(node),
    cut' = cut.union(Set(node)), everCut' = true,
    unchangedLeadership,
  }

  action heal: bool = all { cut' = Set(), everCut' = everCut, unchangedLeadership }

  action campaign(node: str, term: int): bool = all {
    NODES.contains(node),
    maxTerm' = if (term > maxTerm) term else maxTerm,
    cut' = cut, everCut' = everCut, leader' = leader, led' = led, twoLeaders' = twoLeaders, unchangedLogs,
  }

  action lead(node: str, term: int): bool = all {
    NODES.contains(node), term <= maxTerm,
    leader' = leader.put(node, term),
    led' = led.union(Set((node, term))),
    twoLeaders' = (twoLeaders or led.exists(p => p._2 == term and p._1 != node)),
    cut' = cut, everCut' = everCut, maxTerm' = maxTerm, unchangedLogs,
  }

  action stepDown(node: str, term: int): bool = all {
    leader.keys().contains(node), leader.get(node) == term,
    leader' = leader.keys().exclude(Set(node)).mapBy(n => leader.get(n)),
    cut' = cut, everCut' = everCut, maxTerm' = maxTerm, led' = led, twoLeaders' = twoLeaders, unchangedLogs,
  }

  // a node down owes no catch-up until it boots again
  action stop(node: str): bool = all {
    NODES.contains(node),
    leader' = leader.keys().exclude(Set(node)).mapBy(n => leader.get(n)),
    cut' = cut, everCut' = everCut, maxTerm' = maxTerm, led' = led, twoLeaders' = twoLeaders,
    applied' = applied, target' = target.keys().exclude(Set(node)).mapBy(n => target.get(n)),
  }

  // a node starts with its state machine at index (its snapshot's): behind
  // the others, it owes their highest
  action boot(node: str, index: int): bool = all {
    NODES.contains(node),
    applied' = applied.set(node, index),
    target' = if (othersApplied(node) > index) target.put(node, othersApplied(node)) else target.keys().exclude(Set(node)).mapBy(n => target.get(n)),
    cut' = cut, everCut' = everCut, maxTerm' = maxTerm, leader' = leader, led' = led, twoLeaders' = twoLeaders,
  }

  // a node's state machine reaches index
  action apply(node: str, index: int): bool = all {
    NODES.contains(node),
    applied' = applied.set(node, index),
    target' = if (target.keys().contains(node) and index >= target.get(node)) target.keys().exclude(Set(node)).mapBy(n => target.get(n)) else target,
    cut' = cut, everCut' = everCut, maxTerm' = maxTerm, leader' = leader, led' = led, twoLeaders' = twoLeaders,
  }

  action step = nondet n = NODES.oneOf() nondet t = 1.to(4).oneOf()
    any { isolate(n), heal, campaign(n, t), lead(n, t), stepDown(n, t), stop(n), boot(n, t), apply(n, t) }

  val electionSafety: bool = not(twoLeaders)

  // the network was cut, and is whole again
  val healed: bool = everCut and cut == Set()

  // a node leads the newest term, and reaches the others
  val hasLeader: bool = leader.keys().exists(n => leader.get(n) == maxTerm and not(cut.contains(n)))

  // no node that can reach the others is behind (a node cut off cannot
  // catch up until the network heals)
  val caughtUp: bool = target.keys().forall(n => cut.contains(n))
  val behind: bool = not(caughtUp)
}
