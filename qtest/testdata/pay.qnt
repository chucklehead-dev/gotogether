// -*- mode: Bluespec; -*-
// A payment step's I/O specification: paying an order must capture its
// amount (ten a unit) once, retries aside, and then notify the customer.
// The effects are a def over the post-state (payEffects), compared with
// the effects the step made (pay.yaml's effects section).
module pay {
  type Effect =
    | Capture({ order: str, amount: int })
    | Notify({ order: str })

  var orders: str -> int // placed order -> units
  var paid: Set[str]

  action init = all { orders' = Map(), paid' = Set() }

  action place(order: str, qty: int): bool = all {
    not(orders.keys().contains(order)), qty > 0,
    orders' = orders.put(order, qty), paid' = paid,
  }

  action pay(order: str): bool = all {
    orders.keys().contains(order),
    paid' = paid.union(Set(order)), orders' = orders,
  }

  // what paying sends: the capture of the order's amount, then the notice
  def payEffects(order: str): List[Effect] =
    [Capture({ order: order, amount: orders.get(order) * 10 }), Notify({ order: order })]

  // what a refund sends, in any order: the money back and the notice
  def refundEffects(order: str): List[Effect] =
    [Capture({ order: order, amount: 0 - orders.get(order) * 10 }), Notify({ order: order })]

  action refund(order: str): bool = all {
    paid.contains(order), paid' = paid.exclude(Set(order)), orders' = orders,
  }

  action step = {
    nondet order = Set("o1", "o2").oneOf()
    nondet qty = 1.to(2).oneOf()
    any { place(order, qty), pay(order), refund(order) }
  }
}
