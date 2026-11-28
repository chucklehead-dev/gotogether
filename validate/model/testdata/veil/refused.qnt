// A model outside Veil's fragment: the size of a set of unknown elements.
module refused {
  var applied: Set[int]
  var count: int

  action init = all { applied' = Set(), count' = 0 }
  action apply(id: int): bool = all {
    not(applied.contains(id)),
    applied' = applied.union(Set(id)),
    count' = count + 1,
  }
  action step = { nondet id = 1.to(3).oneOf() apply(id) }

  val exactlyOnce = count == applied.size()
}
