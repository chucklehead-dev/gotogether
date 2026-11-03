// Logical transaction admission only: this set is independent of the Store's
// lease counter. Physical SQL sessions, cancellation, crashes and resets are
// outside this bounded model; runtime lifecycle tests cover those boundaries.
module store_capacity {
  const LIMIT: int
  var active: Set[int]

  action init = active' = Set()
  action begin(id: int, admitted: bool): bool = all {
    id.in(1.to(4)),
    not(active.contains(id)),
    admitted == (active.size() < LIMIT),
    active' = if (admitted) active.union(Set(id)) else active,
  }
  // Ending an already-ended logical transaction cannot release another one.
  action finish(id: int): bool = all {
    id.in(1.to(4)),
    active' = active.exclude(Set(id)),
  }
  action step = {
    nondet id = 1.to(4).oneOf()
    nondet admitted = Bool.oneOf()
    any { begin(id, admitted), finish(id) }
  }
  val bounded: bool = active.size() <= LIMIT
}
