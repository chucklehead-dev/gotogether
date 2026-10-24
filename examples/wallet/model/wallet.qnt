// A wallet: accounts whose balances move by deposits and transfers, each
// request applied at most once by its id, and a ledger that posts every
// applied request. What a client sees is checked against it: a request's
// outcome, a balance read, what a probe observed after the system settled.
//
// Two accounts may share one credit line (JOINT): a transfer from either
// is allowed while their sum covers it, so one of them may go negative.
// That is the rule a database at snapshot isolation breaks when two
// transfers, one from each, read both and write only their own (write
// skew).
module wallet {
  const ACCOUNTS: Set[str]
  const JOINT: Set[str]
  const INITIAL: int

  var bal: str -> int
  // each request's outcome, by id, the first time it was applied: a retry
  // of it changes nothing and answers the same
  var result: int -> bool
  // the requests the ledger posted
  var posted: Set[int]

  action init = all {
    bal' = ACCOUNTS.mapBy(_ => INITIAL),
    result' = Map(),
    posted' = Set(),
  }

  // what a transfer from src may spend: its own balance, or its credit
  // line's
  pure def joint(b: str -> int): int = JOINT.fold(0, (s, a) => s + b.get(a))
  pure def funds(b: str -> int, src: str): int = if (JOINT.contains(src)) joint(b) else b.get(src)

  action transfer(id: int, src: str, dest: str, amount: int): bool = all {
    ACCOUNTS.contains(src), ACCOUNTS.contains(dest), src != dest, amount > 0,
    if (result.keys().contains(id)) all { bal' = bal, result' = result }
    else if (funds(bal, src) >= amount) all {
      bal' = bal.setBy(src, x => x - amount).setBy(dest, x => x + amount),
      result' = result.put(id, true),
    } else all { bal' = bal, result' = result.put(id, false) },
    posted' = posted,
  }

  action deposit(id: int, account: str, amount: int): bool = all {
    ACCOUNTS.contains(account), amount > 0,
    if (result.keys().contains(id)) all { bal' = bal, result' = result }
    else all { bal' = bal.setBy(account, x => x + amount), result' = result.put(id, true) },
    posted' = posted,
  }

  // a read changes nothing; the binding checks the balance it returned
  action read(account: str): bool = all {
    ACCOUNTS.contains(account),
    bal' = bal, result' = result, posted' = posted,
  }

  // the ledger posts a request, once. It may be recorded before its
  // request's caller heard back (the ledger records where it commits, the
  // caller when answered), so it commutes with every request: what it
  // posted is judged after the settle (postedApplied, allPosted).
  action post(id: int): bool = all {
    not(posted.contains(id)),
    bal' = bal, result' = result, posted' = posted.union(Set(id)),
  }

  // a probe's read of an account after the system settled
  action observe(account: str, balance: int): bool = all {
    ACCOUNTS.contains(account), bal.get(account) == balance,
    bal' = bal, result' = result, posted' = posted,
  }

  action step = {
    nondet id = 1.to(4).oneOf()
    nondet src = ACCOUNTS.oneOf()
    nondet dest = ACCOUNTS.oneOf()
    nondet amount = 1.to(150).oneOf()
    any {
      transfer(id, src, dest, amount),
      deposit(id, src, amount),
      read(src),
      post(id),
      observe(src, bal.get(src)),
    }
  }

  // no account outside a credit line goes negative, and no credit line
  // does in sum
  val noOverdraft: bool = ACCOUNTS.forall(a => JOINT.contains(a) or bal.get(a) >= 0) and joint(bal) >= 0

  // every applied request was posted, and only those: eventual
  // properties, judged when the run settled
  val allPosted: bool = result.keys().forall(i => not(result.get(i)) or posted.contains(i))
  val postedApplied: bool = posted.forall(i => result.keys().contains(i) and result.get(i))
}
