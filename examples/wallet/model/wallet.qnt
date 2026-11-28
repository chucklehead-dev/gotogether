// A wallet: accounts whose balances move by deposits and transfers, each
// request applied at most once by its id, and a ledger that posts every
// applied request's moves. What a client sees is checked against it: a
// request's outcome, a balance read, what a probe observed after the
// system settled.
//
// Two accounts may share one credit line (JOINT): a transfer from either
// is allowed while their sum covers it, so one of them may go negative.
// That is the rule a database at snapshot isolation breaks when two
// transfers, one from each, read both and write only their own (write
// skew). A request the wallet must refuse (an account it does not hold,
// a transfer to its own source, an amount that is not positive) is
// answered false and moves nothing: it is a request a client can send,
// not one the model rules out.
module wallet {
  const ACCOUNTS: Set[str]
  const JOINT: Set[str]
  const INITIAL: int

  var bal: str -> int
  // each request's outcome, by id, the first time it was applied: a retry
  // of it changes nothing and answers the same
  var result: int -> bool
  // each applied request's moves, by account
  var moves: int -> (str -> int)
  // what the ledger posted of each request: an account's move
  var posted: int -> (str, int)

  action init = all {
    bal' = ACCOUNTS.mapBy(_ => INITIAL),
    result' = Map(),
    moves' = Map(),
    posted' = Map(),
  }

  // what a transfer from src may spend: its own balance, or its credit
  // line's
  val joint: int = JOINT.fold(0, (s, a) => s + bal.get(a))
  def funds(src: str): int = if (JOINT.contains(src)) joint else bal.get(src)

  // a request answered as it was the first time, or refused
  action answer(id: int, ok: bool): bool = all {
    result' = if (result.keys().contains(id)) result else result.put(id, ok),
    bal' = bal, moves' = moves, posted' = posted,
  }

  // a request applied: its moves m, which move amount from src to dest
  // (a transfer) or to dest alone (a deposit)
  action apply(id: int, m: str -> int): bool = all {
    result' = result.put(id, true),
    moves' = moves.put(id, m),
    posted' = posted,
  }
  action move(id: int, src: str, dest: str, amount: int): bool = all {
    bal' = ACCOUNTS.mapBy(a => if (a == src) bal.get(a) - amount else if (a == dest) bal.get(a) + amount else bal.get(a)),
    apply(id, Map(src -> -amount).put(dest, amount)),
  }
  action credit(id: int, dest: str, amount: int): bool = all {
    bal' = ACCOUNTS.mapBy(a => if (a == dest) bal.get(a) + amount else bal.get(a)),
    apply(id, Map(dest -> amount)),
  }

  action transfer(id: int, src: str, dest: str, amount: int): bool =
    if (result.keys().contains(id)) answer(id, false)
    else if (ACCOUNTS.contains(src) and ACCOUNTS.contains(dest) and src != dest and amount > 0 and funds(src) >= amount)
      move(id, src, dest, amount)
    else answer(id, false)

  action deposit(id: int, account: str, amount: int): bool =
    if (result.keys().contains(id)) answer(id, false)
    else if (ACCOUNTS.contains(account) and amount > 0) credit(id, account, amount)
    else answer(id, false)

  // a read changes nothing; the binding checks the balance it returned
  action read(account: str): bool = all {
    ACCOUNTS.contains(account),
    bal' = bal, result' = result, moves' = moves, posted' = posted,
  }

  // the ledger posts a request's move on an account, once. It may be
  // recorded before its request's caller heard back (the ledger records
  // where it commits, the caller when answered), so it commutes with
  // every request: what it posted is judged after the settle
  // (postedApplied, allPosted).
  action post(id: int, account: str, amount: int): bool = all {
    not(posted.keys().contains(id)),
    bal' = bal, result' = result, moves' = moves, posted' = posted.put(id, (account, amount)),
  }

  // a probe's read of an account after the system settled: the model holds
  // what it saw, or the probe's step is no transition
  action observe(account: str, balance: int): bool = all {
    ACCOUNTS.contains(account), bal.get(account) == balance,
    bal' = bal, result' = result, moves' = moves, posted' = posted,
  }

  action step = {
    nondet id = 1.to(4).oneOf()
    nondet src = ACCOUNTS.oneOf()
    nondet dest = ACCOUNTS.oneOf()
    nondet amount = (-10).to(150).oneOf()
    any {
      transfer(id, src, dest, amount),
      deposit(id, src, amount),
      read(src),
      all { moves.keys().contains(id), nondet a = moves.get(id).keys().oneOf() post(id, a, moves.get(id).get(a)) },
      observe(src, bal.get(src)),
    }
  }

  // no account outside a credit line goes negative, and no credit line
  // does in sum. The model keeps it by construction: in a run it fails only
  // if the run's steps drive the model there, which a refused transfer the
  // bank applied would show first as an outcome or a balance it cannot be.
  val noOverdraft: bool = ACCOUNTS.forall(a => JOINT.contains(a) or bal.get(a) >= 0) and joint >= 0

  // every applied request was posted, and every post is one of its
  // request's moves: eventual properties, judged when the run settled
  val allPosted: bool = moves.keys().forall(i => posted.keys().contains(i))
  val postedApplied: bool = posted.keys().forall(i =>
    moves.keys().contains(i) and moves.get(i).keys().contains(posted.get(i)._1) and moves.get(i).get(posted.get(i)._1) == posted.get(i)._2)
}

// The binding's instance, which Veil proves noOverdraft of
// (simulate.inductive): for every request, amount and order, but for
// these constants only. joint's fold over JOINT is outside Veil's fragment
// for every value of JOINT ("a set fold without known finite support"),
// within it for a literal set.
module walletInstance {
  import wallet(ACCOUNTS = Set("a1", "a2", "b", "c"), JOINT = Set("a1", "a2"), INITIAL = 100).*
}
