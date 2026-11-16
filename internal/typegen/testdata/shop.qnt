// A shop whose state has every kind of type gotogether types maps: the
// model of internal/typegen/shoptypes, generated from it (go generate
// there) and checked against it (cmd/gotogether's TestTypesCheck).
module shop {
  type Option[a] = Some(a) | None
  // polymorphic: written out where it is used
  type Tagged[a] = { label: str, value: a }
  // uninterpreted: no value of it is ever made
  type Ref

  type Phase = Idle | Open | Closed
  type Item = { sku: str, qty: int }
  type Slot = { shelf: int, row: int }
  type Event = Added(Item) | Removed(str) | Moved((Slot, Slot)) | Cleared
  type Order = { id: int, items: List[Item], note: Option[str], phase: Phase }
  type Amount = int

  var phase: Phase
  var orders: Set[Order]
  var stock: Slot -> Item
  var byName: str -> Amount
  var groups: Set[str] -> Phase
  var last: Option[Event]
  var pair: (int, str)
  var log: List[Event]
  var tagged: Tagged[Option[int]]

  action init = all {
    phase' = Idle,
    orders' = Set(),
    stock' = Map(),
    byName' = Map(),
    groups' = Map(),
    last' = None,
    pair' = (0, ""),
    log' = [],
    tagged' = { label: "", value: None },
  }

  action add(sku: str, qty: int): bool = all {
    val it = { sku: sku, qty: qty }
    all {
      phase' = Open,
      orders' = orders.union(Set({ id: orders.size(), items: [it], note: Some(sku), phase: Open })),
      stock' = stock.put({ shelf: qty, row: 1 }, it),
      byName' = byName.put(sku, qty),
      groups' = groups.put(Set(sku, "all"), Open),
      last' = Some(Added(it)),
      pair' = (qty, sku),
      log' = log.append(Added(it)),
      tagged' = { label: sku, value: Some(qty) },
    }
  }

  action move = all {
    phase' = Closed,
    orders' = orders.union(Set({ id: orders.size(), items: [], note: None, phase: Closed })),
    stock' = stock,
    byName' = byName,
    groups' = groups,
    last' = Some(Moved(({ shelf: 1, row: 2 }, { shelf: 3, row: 4 }))),
    pair' = pair,
    log' = log.append(Moved(({ shelf: 1, row: 2 }, { shelf: 3, row: 4 }))),
    tagged' = tagged,
  }

  action drop(sku: str): bool = all {
    phase' = phase,
    orders' = orders,
    stock' = stock,
    byName' = byName,
    groups' = groups,
    last' = Some(Removed(sku)),
    pair' = pair,
    log' = log.append(Removed(sku)).append(Cleared),
    tagged' = tagged,
  }

  action step = {
    nondet sku = Set("a", "b").oneOf()
    nondet qty = 1.to(3).oneOf()
    any {
      add(sku, qty),
      move,
      drop(sku),
    }
  }
}
