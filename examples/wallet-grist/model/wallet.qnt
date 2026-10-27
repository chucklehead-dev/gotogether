// examples/wallet-grist: examples/wallet's bank on grist, as one tenant's
// log: the rules and invariants grist's generated skeleton (wallet_gen.qnt,
// `wallet model`: the events, the actions of every block, the frame
// conditions, step) calls. examples/wallet/model/wallet.qnt keeps
// balances, each request's outcome and its moves as variables; here they
// are defs over the log, each request's outcome is grist's marker, and the
// ledger's posts are the post processor's effect steps and the ledger's
// own record of what it posted (its site, ledgerPost).

// The rules: the bank's accounts, its read model, each command's outcome,
// events and needs, the read's answer, the post's event and the ledger's
// dedupe.
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
  // the bank does not hold)
  pure def balanceOf(log: List[Event], a: str): int =
    if (ACCOUNTS.contains(a)) balancesEvents(log, a).foldl(INITIAL, (s, e) => s + movedAmount(e)) else 0
  // what a transfer from src may spend: its own balance, or its credit
  // line's
  pure def funds(log: List[Event], src: str): int =
    if (JOINT.contains(src)) JOINT.fold(0, (s, a) => s + balanceOf(log, a)) else balanceOf(log, src)

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
  // the ledger posts once per key (it deduplicates; a ledger that does not
  // posts a retried move twice, which this rule refuses)
  pure def ledgerPostAdmits(records: List[LedgerPostRecord], r: LedgerPostRecord): bool =
    records.select(p => p.key == r.key).length() == 0

  // no account outside the credit line goes negative, nor the line in sum
  pure def noOverdraftIn(log: List[Event]): bool =
    ACCOUNTS.forall(a => JOINT.contains(a) or balanceOf(log, a) >= 0) and
    JOINT.fold(0, (s, a) => s + balanceOf(log, a)) >= 0
}

// The store's side (wallet.binding.yaml and its variant).
module wallet {
  import grist.* from "./grist"
  import walletEvents.* from "./wallet_gen"
  import walletRules.*
  import walletGen.* from "./wallet_gen"
  export walletGen.*

  // the design's: no overdraft, in any state
  // grist: invariant
  val noOverdraft = noOverdraftIn(log)

  // every move was posted (an ok effect step, checkpointed), or its post
  // failed every try and says so
  // grist: eventually
  val allPosted =
    log.indices().forall(i => log[i].typ == "moved" implies and {
      checkpoints.get("post") >= i + 1,
      effectsOf(effects, "post", i + 1, true) > 0 or holds(log, failed("post", i + 1)),
    })

  // every post the ledger made is a move's, under that move's effect key
  // (the ledger records before its answer is the effect's step, so this
  // is judged after the settle)
  // grist: eventually
  val postedApplied =
    ledgerPostRecords.foldl(true, (b, p) => b and effects.indices().exists(j => and {
      effects[j]._1 == "post",
      effects[j]._3 == p.key,
      movedAccount(log[effects[j]._2 - 1]) == p.account,
      movedAmount(log[effects[j]._2 - 1]) == p.amount,
    }))

  // test-only: the ledger holds every move exactly once, under its effect
  // key. At least once allows a post given up after EFFECT_TRIES failures
  // (grist.failed); a run without ledger faults must not have one.
  // grist: eventually exactly
  val everyMovePostedOnce =
    log.indices().forall(i => log[i].typ == "moved" implies
      effects.indices().exists(j => effects[j]._1 == "post" and effects[j]._2 == i + 1 and
        ledgerPostRecords.select(p => p.key == effects[j]._3).length() == 1))
}

// The clients' side (wallet.calls.binding.yaml), linearized.
module walletCalls {
  import grist.* from "./grist"
  import walletEvents.* from "./wallet_gen"
  import walletRules.*
  import walletCallsGen.* from "./wallet_gen"
  export walletCallsGen.*

  // grist: invariant
  val noOverdraft = noOverdraftIn(log)
}
