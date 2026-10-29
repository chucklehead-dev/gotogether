// A system specification independent of each App's generated local model.
// Each instance is one tenant; identities are committed command ids.
module chain {
  type Payload = { order: str, amount: int }
  var orders: str -> Payload
  var payments: str -> str
  var paid: Set[str]
  var posted: Set[str]

  action init = all {
    orders' = Map(), payments' = Map(), paid' = Set(), posted' = Set(),
  }

  action ordersPlace(id: str, order: str, amount: int): bool = all {
    not(orders.keys().contains(id)),
    orders' = orders.put(id, { order: order, amount: amount }),
    payments' = payments, paid' = paid, posted' = posted,
  }

  // A payment must depend on the accepted order whose payload it pays.
  // A new command id cannot make paying that order twice legal.
  action bankPay(id: str, parentApp: str, parentID: str, order: str, amount: int): bool = {
    all {
      if (parentApp == "orders" and orders.keys().contains(parentID)) and {
        orders.get(parentID) == { order: order, amount: amount },
        not(payments.keys().contains(id)), not(paid.contains(parentID)),
      } else false,
      payments' = payments.put(id, parentID), paid' = paid.union(Set(parentID)),
      orders' = orders, posted' = posted,
    }
  }

  // A posting must depend on an actual payment, not merely an order.
  // This rejects a direct orders -> ledger hop even if its end-state
  // totals happen to agree with a correctly ordered execution.
  action ledgerPost(id: str, parentApp: str, parentID: str, order: str, amount: int): bool = {
    val requested = if (payments.keys().contains(parentID)) payments.get(parentID) else ""
    all {
      if (parentApp == "bank" and payments.keys().contains(parentID) and orders.keys().contains(requested)) and {
        orders.get(requested) == { order: order, amount: amount },
        not(posted.contains(requested)),
      } else false,
      posted' = posted.union(Set(requested)),
      orders' = orders, payments' = payments, paid' = paid,
    }
  }

  // Only at a recorded settle: finite prefixes owe work by right.
  val allPosted = posted == orders.keys()
}
