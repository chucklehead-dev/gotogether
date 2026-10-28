// Orders placed over gRPC are synced from the log: an order may be placed
// again (a client's retry, a duplicated request) and synced again (the
// log delivers at least once), but nothing is synced that was not placed.
module ordersync {
  const ORDERS: Set[int]

  var placed: Set[int]
  var synced: Set[int]

  action init = all { placed' = Set(), synced' = Set() }

  action place(id: int): bool = all {
    ORDERS.contains(id),
    placed' = placed.union(Set(id)),
    synced' = synced,
  }

  action sync(id: int): bool = all {
    placed.contains(id),
    synced' = synced.union(Set(id)),
    placed' = placed,
  }

  action step = { nondet id = ORDERS.oneOf() any { place(id), sync(id) } }

  val syncedPlaced = synced.subseteq(placed)
}
