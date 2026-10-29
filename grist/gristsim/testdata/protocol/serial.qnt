// Deliberately overconstrained: a valid recorded two-order history can be
// serialized to satisfy this model, but the evidence also admits concurrency.
module serial {
  var open: bool
  action init = open' = false
  action ordersPlace(id: str, order: str, amount: int): bool = all {
    not(open), open' = true,
  }
  action bankPay(id: str, order: str, amount: int): bool = open' = open
  action ledgerPost(id: str, order: str, amount: int): bool = all {
    open, open' = false,
  }
  val allPosted = not(open)
}
