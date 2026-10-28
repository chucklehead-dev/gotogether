// Accounts a serial log opens, credits and debits, each step one
// account's (partition_test.go): the model factors by the account, but
// for transfer (two accounts' entries), census (every account's),
// totalBounded (a sum over every account) and aCovers (each account
// judged with a's).
module ledger {
  const ACCTS: Set[str]
  const LIMIT: int
  var bal: str -> int

  action init = bal' = Map()

  action open(acct: str): bool = all { not(bal.keys().contains(acct)), bal' = bal.put(acct, 0) }
  action deposit(acct: str, amount: int): bool = all {
    bal.keys().contains(acct), amount > 0, amount <= LIMIT, bal' = bal.setBy(acct, b => b + amount),
  }
  // the model allows any debit of an open account: the code's to keep it
  // covered (nonNegative)
  action withdraw(acct: str, amount: int): bool = all {
    bal.keys().contains(acct), amount > 0, bal' = bal.setBy(acct, b => b - amount),
  }
  action transfer(acct: str, dest: str, amount: int): bool = all {
    bal.keys().contains(acct), bal.keys().contains(dest), acct != dest, amount > 0, amount <= bal.get(acct),
    bal' = bal.setBy(acct, b => b - amount).setBy(dest, b => b + amount),
  }
  action census(acct: str, n: int): bool = all { bal.keys().size() == n, bal' = bal }

  action step = {
    nondet a = ACCTS.oneOf()
    nondet d = ACCTS.oneOf()
    nondet n = 1.to(LIMIT).oneOf()
    any { open(a), deposit(a, n), withdraw(a, n), transfer(a, d, n), census(a, n) }
  }

  val nonNegative = bal.keys().forall(a => bal.get(a) >= 0)
  // every account judged with a's: an entry of another entity's
  val aCovers = bal.keys().forall(x => not(bal.keys().contains("a")) or bal.get(x) + bal.get("a") >= 0)
  val totalBounded = bal.keys().fold(0, (s, a) => s + bal.get(a)) <= 100 * LIMIT
}
