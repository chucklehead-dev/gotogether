// Every ring edge joins adjacent accounts. Bounds are entry-local, while
// conservation judges the entire map after each transfer.
module interacting_wide {
  const INITIAL: str -> int
  const TOTAL: int
  var bal: str -> int

  action init = bal' = INITIAL
  action transfer(src: str, dest: str, amount: int): bool = all {
    src != dest, amount > 0, bal.get(src) >= amount,
    bal' = bal.setBy(src, b => b - amount).setBy(dest, b => b + amount),
  }
  // Controlled destination fault: balances remain bounded, but money is made.
  action extraCredit(src: str, dest: str, amount: int): bool = all {
    src != dest, amount > 0, bal.get(src) >= amount,
    bal' = bal.setBy(src, b => b - amount).setBy(dest, b => b + amount + 1),
  }
  val bounded = bal.keys().forall(acct => bal.get(acct) >= 0 and bal.get(acct) <= 20)
  val conserved = bal.keys().fold(0, (sum, acct) => sum + bal.get(acct)) == TOTAL
  action step = transfer("acct-000000", "acct-000001", 1)
}
