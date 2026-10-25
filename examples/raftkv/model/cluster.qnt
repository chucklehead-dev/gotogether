// Raft's leadership as the nodes record it: a node campaigns for a term,
// leads it, steps down from it, or stops (crashes); the test cuts a node
// off and heals the network. Two properties:
//
//   - electionSafety: at most one node leads a term, ever (raft's
//     Election Safety), kept by a ghost that remembers every term led.
//   - after the network heals, some node leads the newest term and is not
//     cut off (hasLeader), within a bound the binding gives.
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

  action init = all {
    cut' = Set(), everCut' = false, maxTerm' = 1, leader' = Map(), led' = Set(), twoLeaders' = false,
  }

  action unchangedLeadership = all { maxTerm' = maxTerm, leader' = leader, led' = led, twoLeaders' = twoLeaders }

  action isolate(node: str): bool = all {
    NODES.contains(node),
    cut' = cut.union(Set(node)), everCut' = true,
    unchangedLeadership,
  }

  action heal: bool = all { cut' = Set(), everCut' = everCut, unchangedLeadership }

  action campaign(node: str, term: int): bool = all {
    NODES.contains(node),
    maxTerm' = if (term > maxTerm) term else maxTerm,
    cut' = cut, everCut' = everCut, leader' = leader, led' = led, twoLeaders' = twoLeaders,
  }

  action lead(node: str, term: int): bool = all {
    NODES.contains(node), term <= maxTerm,
    leader' = leader.put(node, term),
    led' = led.union(Set((node, term))),
    twoLeaders' = (twoLeaders or led.exists(p => p._2 == term and p._1 != node)),
    cut' = cut, everCut' = everCut, maxTerm' = maxTerm,
  }

  action stepDown(node: str, term: int): bool = all {
    leader.keys().contains(node), leader.get(node) == term,
    leader' = leader.keys().exclude(Set(node)).mapBy(n => leader.get(n)),
    cut' = cut, everCut' = everCut, maxTerm' = maxTerm, led' = led, twoLeaders' = twoLeaders,
  }

  action stop(node: str): bool = all {
    NODES.contains(node),
    leader' = leader.keys().exclude(Set(node)).mapBy(n => leader.get(n)),
    cut' = cut, everCut' = everCut, maxTerm' = maxTerm, led' = led, twoLeaders' = twoLeaders,
  }

  action step = nondet n = NODES.oneOf() nondet t = 1.to(4).oneOf()
    any { isolate(n), heal, campaign(n, t), lead(n, t), stepDown(n, t), stop(n) }

  val electionSafety: bool = not(twoLeaders)

  // the network was cut, and is whole again
  val healed: bool = everCut and cut == Set()

  // a node leads the newest term, and reaches the others
  val hasLeader: bool = leader.keys().exists(n => leader.get(n) == maxTerm and not(cut.contains(n)))
}
