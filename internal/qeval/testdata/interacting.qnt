// A nonpartitionable ledger: each transfer changes two accounts. Bounds
// are entry-local; backed also depends on another account's balance.
module interacting {
  var bal: str -> int
  action init = bal' = Map()
  action open(acct: str, amount: int): bool = all {
    not(bal.keys().contains(acct)), bal' = bal.put(acct, amount),
  }
  action transfer(src: str, dest: str, amount: int): bool = all {
    src != dest, amount > 0,
    bal' = bal.setBy(src, b => b - amount).setBy(dest, b => b + amount),
  }
  action reset = bal' = Map()
  def boundedAt(acct: str): bool = bal.get(acct) >= 0 and bal.get(acct) <= 20
  val bounded = bal.keys().forall(acct => boundedAt(acct))
  def backedAt(acct: str): bool = bal.get(acct) <= bal.get("reserve")
  val backed = bal.keys().forall(acct => backedAt(acct))
  action step = transfer("a", "b", 1)
}
