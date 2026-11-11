// wallet_rules.qnt's balances against the definitions they replaced
// (docs/performance.md, wallet-grist's noOverdraft proof): the earlier
// definitions, verbatim, and a machine that grows a log of every event
// the models append, whose invariant is that each new definition equals
// its earlier one at every account. The models' logs hold only events
// their generated constructors make (moved, posted, grist.failed, each
// batch stamped with its cause by caused), so a moved event's account
// (its first string, movedAccount) is the account it is tagged with
// (balancesEvents' selection): the machine makes events the same way,
// with every account, amount and cause. sim_test.go samples it (quint
// run); quint verify checks it for every log of a bounded length; and two
// planted mistakes (planted) show that the check fails when a new
// definition is wrong.
module balancesOld {
  import walletEvents.* from "../model/wallet_gen"
  import walletRules.* from "../model/wallet_rules"

  // the earlier definitions
  pure def oldBalanceOf(log: List[Event], a: str): int =
    if (ACCOUNTS.contains(a)) balancesEvents(log, a).foldl(INITIAL, (s, e) => s + movedAmount(e)) else 0
  pure def oldFunds(log: List[Event], src: str): int =
    if (JOINT.contains(src)) JOINT.fold(0, (s, a) => s + oldBalanceOf(log, a)) else oldBalanceOf(log, src)
  pure def oldNoOverdraftIn(log: List[Event]): bool =
    ACCOUNTS.forall(a => JOINT.contains(a) or oldBalanceOf(log, a) >= 0) and
    JOINT.fold(0, (s, a) => s + oldBalanceOf(log, a)) >= 0

  // each definition the rules and invariants read equals its earlier one
  // on log: the balances (readAnswer's), the funds (transferOutcome's
  // only read of the log), the credit line's sum and the invariant itself
  pure def agreeOn(log: List[Event]): bool = and {
    STRS.forall(a => balanceOf(log, a) == oldBalanceOf(log, a)),
    STRS.forall(src => funds(log, src) == oldFunds(log, src)),
    lineBalance(log) == JOINT.fold(0, (s, a) => s + oldBalanceOf(log, a)),
    noOverdraftIn(log) == oldNoOverdraftIn(log),
  }
}

module balances {
  import walletEvents.* from "../model/wallet_gen"
  import walletRules.* from "../model/wallet_rules"
  import balancesOld.*

  // amounts as moves carry them: a deposit's, and both sides of a
  // transfer's (-amount at the source)
  pure val AMOUNTS = (-6).to(6)
  pure val CAUSES: Set[Cause] = Set(("command", "i1"), ("command", "i2"), ("processor", "post"))

  var log: List[Event]

  action init = log' = []
  action step = any {
    {
      nondet a = STRS.oneOf()
      nondet n = AMOUNTS.oneOf()
      nondet c = CAUSES.oneOf()
      log' = log.concat(caused([moved(a, n)], c))
    },
    {
      nondet a = STRS.oneOf()
      nondet n = AMOUNTS.oneOf()
      nondet t = 1.to(3).oneOf()
      log' = log.concat(caused([posted(a, n, t)], ("processor", "post")))
    },
    {
      nondet t = 1.to(3).oneOf()
      log' = log.append(failed("post", t))
    },
  }

  val agree = agreeOn(log)

  // planted mistakes, each a definition the check must reject: the credit
  // line summed over every account, and a balance that adds every
  // account-tagged event, posted ones included
  pure def plantedLine(log: List[Event]): int =
    log.foldl(JOINT.size() * INITIAL, (s, e) => if (e.typ == "moved" and ACCOUNTS.contains(movedAccount(e))) s + movedAmount(e) else s)
  pure def plantedBalanceOf(log: List[Event], a: str): int =
    if (ACCOUNTS.contains(a)) log.foldl(INITIAL, (s, e) => if (e.strs[0] == a) s + e.ints[0] else s) else 0
  val plantedLineAgrees = plantedLine(log) == JOINT.fold(0, (s, a) => s + oldBalanceOf(log, a))
  val plantedBalanceAgrees = STRS.forall(a => plantedBalanceOf(log, a) == oldBalanceOf(log, a))
}

// the same equalities in every state the models reach (sampled by
// sim_test.go): the store's side and the clients'
module balancesStore {
  import wallet.* from "../model/wallet"
  import balancesOld.*
  val agree = agreeOn(log)
}

module balancesCalls {
  import walletCalls.* from "../model/wallet"
  import balancesOld.*
  val agree = agreeOn(log)
}
