// A vending machine (P3's possibility checks): coins in, a drink out
// at a price of PRICE, refunds. Its state space is small enough for the
// Go evaluator's search to see all of it.
module vending {
  const PRICE: int
  const STOCK: int
  var credit: int
  var stock: int
  var sold: int
  var last: str

  action init = all { credit' = 0, stock' = STOCK, sold' = 0, last' = "" }

  action insert(coin: int): bool = all {
    credit + coin <= 3,
    credit' = credit + coin, stock' = stock, sold' = sold, last' = "insert",
  }

  // a purchase's result is the machine's: empty, short of credit, or ok
  action buy(result: str): bool = all {
    result == (if (stock == 0) "empty" else if (credit < PRICE) "short" else "ok"),
    credit' = if (result == "ok") credit - PRICE else credit,
    stock' = if (result == "ok") stock - 1 else stock,
    sold' = if (result == "ok") sold + 1 else sold,
    last' = result,
  }

  action refund = all { credit > 0, credit' = 0, stock' = stock, sold' = sold, last' = "refund" }

  // a repair is for a machine whose stock went negative: never
  action repair = all { stock < 0, credit' = credit, stock' = 0, sold' = sold, last' = "repair" }

  action step = any {
    { nondet coin = 1.to(2).oneOf() insert(coin) },
    { nondet r = Set("ok", "empty", "short").oneOf() buy(r) },
    refund,
    repair,
  }

  val soldOut = stock == 0
  val nonNegative = credit >= 0 and stock >= 0
}
