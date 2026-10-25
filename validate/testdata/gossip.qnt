// Nodes on a line that learn values and gossip them to their neighbours,
// the gossip never recorded (hidden): a read may see what gossip brought,
// a value two hops away taking two rounds (hidden_test.go).
module gossip {
  pure val NODES = Set("a", "b", "c")
  var known: str -> Set[int]

  action init = known' = NODES.mapBy(_ => Set())

  action write(node: str, value: int): bool = all {
    NODES.contains(node),
    known' = known.set(node, known.get(node).union(Set(value))),
  }

  action read(node: str): bool = all {
    NODES.contains(node),
    known' = known,
  }

  // neighbours on a line: a - b - c
  pure val LINKS = Set(("a", "b"), ("b", "a"), ("b", "c"), ("c", "b"))

  // one round: a node tells a neighbour all it knows
  action gossipAny = {
    nondet l = LINKS.oneOf()
    known' = known.set(l._2, known.get(l._2).union(known.get(l._1)))
  }

  action step = any {
    nondet n = NODES.oneOf() nondet v = 1.to(3).oneOf() write(n, v),
    nondet n = NODES.oneOf() read(n),
    gossipAny,
  }

  val small = NODES.forall(n => known.get(n).size() <= 2)
}
