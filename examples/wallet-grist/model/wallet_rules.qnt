// examples/wallet-grist's rules: the holes grist's generated skeleton
// (wallet_steps.qnt, `wallet model`) calls.

// The rules: the bank's accounts, its read model, each command's outcome,
// events and needs, the read's answer, the post's event and its request of
// the ledger.
module walletRules {
  import grist.* from "./grist"
  import walletEvents.* from "./wallet_gen"

  pure val STRS: Set[str] = Set("a", "b", "c", "z")
  pure val INTS: Set[int] = (-1).to(6)

  // the accounts the bank holds, each opening with INITIAL (cents); JOINT
  // share one credit line. The app states the same (wallet.go).
  pure val ACCOUNTS = Set("a", "b", "c")
  pure val JOINT = Set("a", "b")
  pure val INITIAL = 5

  // balances, keyed by account: INITIAL and its moves (0 for an account
  // the bank does not hold). One fold over the log, adding each move of
  // the account (a moved event's account is its tag's: moved, the only
  // constructor, sets both), not the read model's events selected first
  // (balancesEvents) and then folded: quint verify (Apalache) encodes a
  // select over the symbolic log as a list of symbolic length, which made
  // the noOverdraft proof take hours (docs/performance.md, wallet-grist's
  // noOverdraft proof). testdata/balances.qnt keeps the
  // earlier definitions and checks they agree.
  pure def balanceOf(log: List[Event], a: str): int =
    if (ACCOUNTS.contains(a)) log.foldl(INITIAL, (s, e) => if (e.typ == "moved" and movedAccount(e) == a) s + movedAmount(e) else s) else 0
  // the credit line's balance: the sum of its accounts' (JOINT is a subset
  // of ACCOUNTS), as one fold adding each move of one of them. A sum of
  // per-account folds asks the solver to split every event on each
  // account; one condition per event does not
  pure def lineBalance(log: List[Event]): int =
    log.foldl(JOINT.size() * INITIAL, (s, e) => if (e.typ == "moved" and JOINT.contains(movedAccount(e))) s + movedAmount(e) else s)
  // what a transfer from src may spend: its own balance, or its credit
  // line's
  pure def funds(log: List[Event], src: str): int =
    if (JOINT.contains(src)) lineBalance(log) else balanceOf(log, src)

  // a transfer the bank refuses whatever the balances (before any read),
  // else one its funds do not cover
  pure def validTransfer(c: TransferArgs): bool =
    ACCOUNTS.contains(c.src) and ACCOUNTS.contains(c.dest) and c.src != c.dest and c.amount > 0
  pure def transferOutcome(log: List[Event], c: TransferArgs): str =
    if (not(validTransfer(c))) "invalid" else if (funds(log, c.src) >= c.amount) "ok" else "insufficient-funds"
  pure def transferEvents(log: List[Event], c: TransferArgs): List[Event] = [moved(c.src, -c.amount), moved(c.dest, c.amount)]
  // what a valid transfer's rule reads: the source's moves, or every
  // account's of its credit line. A committed step's read set must cover
  // each, so a decision that reads one account of the line and not the
  // other is caught at every commit
  pure def transferNeeds(c: TransferArgs, result: str): Set[Read] =
    if (result == "invalid") Set() else if (JOINT.contains(c.src)) JOINT.map(a => movedByAccount(a)) else Set(movedByAccount(c.src))

  // a deposit reads nothing: refused when invalid, else its move
  pure def depositOutcome(log: List[Event], c: DepositArgs): str =
    if (ACCOUNTS.contains(c.account) and c.amount > 0) "ok" else "invalid"
  pure def depositEvents(log: List[Event], c: DepositArgs): List[Event] = [moved(c.account, c.amount)]
  pure def depositNeeds(c: DepositArgs, result: str): Set[Read] = Set()

  pure def readAnswer(log: List[Event], q: ReadArgs): ReadResult = { balance: balanceOf(log, q.account) }

  // the ledger's confirmation of the move at the trigger
  pure def postSuccess(prefix: List[Event], trigger: int): List[Event] =
    [posted(movedAccount(prefix[trigger - 1]), movedAmount(prefix[trigger - 1]), trigger)]
  // post skips no move (its handler never Skips)
  pure def postSkips(prefix: List[Event], trigger: int): bool = false
  // what the post asks the ledger for the move at the trigger: its account
  // and amount (the ledger posts each key once: PostEntry is Keyed, and the
  // generated ledgerPostAdmits says so)
  pure def postLedgerPost(prefix: List[Event], trigger: int): LedgerPostRequest =
    { account: movedAccount(prefix[trigger - 1]), amount: movedAmount(prefix[trigger - 1]) }

  // no account outside the credit line goes negative, nor the line in sum
  pure def noOverdraftIn(log: List[Event]): bool =
    ACCOUNTS.forall(a => JOINT.contains(a) or balanceOf(log, a) >= 0) and
    lineBalance(log) >= 0
}
