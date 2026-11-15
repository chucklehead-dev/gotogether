// counters_rules.qnt's one-pass folds against the definitions they
// replaced (docs/performance.md, counters' proofs): the earlier
// definitions, verbatim, and a machine that grows a log of every event
// the models append, whose invariant is that each new definition equals
// its earlier one at every counter, name and caller. The models' logs hold
// only events their generated constructors make (counterCreated,
// counterIncremented, averageComputed, notified, counterRetired, paidOut,
// grist.failed and grist.unknown, each batch stamped with its cause by
// caused), and each constructor tags an event with the strings it stores:
// an increment's counter, a creation's counter and name (their first
// strings) are the ones it is tagged with (totalsEvents' selection,
// existsTagged's match). The machine makes events the same way, with every
// counter, name, owner, amount and cause. rules_test.go samples it (quint
// run); quint verify checks it for every log of a bounded length; and two
// planted mistakes (planted) show that the check fails when a new
// definition is wrong.
module rulesOld {
  import countersEvents.* from "../model/counters_gen"
  import countersRules.* from "../model/counters_rules"

  // the earlier definitions
  pure def oldTotal(log: List[Event], counter: str): int =
    totalsEvents(log, counter).foldl(0, (s, e) => s + counterIncrementedBy(e))
  pure def oldCreatedBy(log: List[Event], counter: str, caller: str): bool =
    log.select(e => e.typ == "counter-created" and counterCreatedCounter(e) == counter and counterCreatedOwner(e) == caller).length() > 0
  pure def oldIsCounter(log: List[Event], counter: str): bool = existsTagged(log, "counter-created", "counter", counter)
  pure def oldCreateCounterOutcome(log: List[Event], c: CreateCounterArgs): str =
    if (existsTagged(log, "counter-created", "name", c.name)) "name-taken" else "ok"
  pure def oldIncrementCounterOutcome(log: List[Event], c: IncrementCounterArgs): str =
    if (oldIsCounter(log, c.counter)) "ok" else "unknown-counter"
  pure def oldRetireCounterOutcome(log: List[Event], c: RetireCounterArgs): str =
    if (not(oldIsCounter(log, c.counter))) "unknown-counter" else if (oldCreatedBy(log, c.counter, c.caller)) "ok" else "not-creator"
  pure def oldRetireCounterEvents(log: List[Event], c: RetireCounterArgs): List[Event] = [counterRetired(c.counter, oldTotal(log, c.counter))]
  pure def oldAverageEmits(prefix: List[Event], trigger: int): List[Event] =
    val incs = prefix.select(e => e.typ == "counter-incremented")
    List(averageComputed(incs.foldl(0, (s, e) => s + counterIncrementedBy(e)), incs.length()))

  // each definition the rules and the query read equals its earlier one
  // on log (averageEmits' prefix is any log: the machine's are every one)
  pure def agreeOn(log: List[Event]): bool = and {
    STRS.forall(c => total(log, c) == oldTotal(log, c) and isCounter(log, c) == oldIsCounter(log, c)),
    STRS.forall(c => STRS.forall(caller => and {
      createdBy(log, c, caller) == oldCreatedBy(log, c, caller),
      retireCounterOutcome(log, { counter: c, caller: caller }) == oldRetireCounterOutcome(log, { counter: c, caller: caller }),
      retireCounterEvents(log, { counter: c, caller: caller }) == oldRetireCounterEvents(log, { counter: c, caller: caller }),
      INTS.forall(by => incrementCounterOutcome(log, { counter: c, by: by, caller: caller }) == oldIncrementCounterOutcome(log, { counter: c, by: by, caller: caller })),
      STRS.forall(n => createCounterOutcome(log, { counter: c, name: n, caller: caller }) == oldCreateCounterOutcome(log, { counter: c, name: n, caller: caller })),
    })),
    averageEmits(log, log.length()) == oldAverageEmits(log, log.length()),
  }
}

module rules {
  import countersEvents.* from "../model/counters_gen"
  import countersRules.* from "../model/counters_rules"
  import rulesOld.*

  pure val CAUSES: Set[Cause] = Set(("command", "i1"), ("command", "i2"), ("processor", "average"))
  // amounts beyond the models' INTS, negative ones included: the folds'
  // sums must agree on any
  pure val AMOUNTS: Set[int] = (-2).to(3)

  var log: List[Event]

  action init = log' = []
  action step = any {
    {
      nondet c = STRS.oneOf()
      nondet n = STRS.oneOf()
      nondet o = STRS.oneOf()
      nondet k = CAUSES.oneOf()
      log' = log.concat(caused([counterCreated(c, n, o)], k))
    },
    {
      nondet c = STRS.oneOf()
      nondet by = AMOUNTS.oneOf()
      nondet k = CAUSES.oneOf()
      log' = log.concat(caused([counterIncremented(c, by)], k))
    },
    {
      nondet sum = AMOUNTS.oneOf()
      nondet count = 0.to(3).oneOf()
      log' = log.concat(caused([averageComputed(sum, count)], ("processor", "average")))
    },
    {
      nondet c = STRS.oneOf()
      log' = log.concat(caused([notified(c)], ("processor", "welcome")))
    },
    {
      nondet c = STRS.oneOf()
      nondet total = AMOUNTS.oneOf()
      nondet k = CAUSES.oneOf()
      log' = log.concat(caused([counterRetired(c, total)], k))
    },
    {
      nondet c = STRS.oneOf()
      nondet t = 1.to(3).oneOf()
      log' = log.concat(caused([paidOut(c, t)], ("processor", "payout")))
    },
    {
      nondet p = Set("welcome", "payout").oneOf()
      nondet t = 1.to(3).oneOf()
      log' = log.append(failed(p, t))
    },
    {
      nondet t = 1.to(3).oneOf()
      log' = log.append(unknown("payout", t, "lost"))
    },
  }

  val agree = agreeOn(log)

  // planted mistakes, each a definition the check must reject: a total
  // that ignores its counter, and a creator check that ignores the caller
  pure def plantedTotal(log: List[Event], counter: str): int =
    log.foldl(0, (s, e) => if (e.typ == "counter-incremented") s + counterIncrementedBy(e) else s)
  pure def plantedCreatedBy(log: List[Event], counter: str, caller: str): bool =
    log.foldl(false, (found, e) => found or (e.typ == "counter-created" and counterCreatedCounter(e) == counter))
  val plantedTotalAgrees = STRS.forall(c => plantedTotal(log, c) == oldTotal(log, c))
  val plantedCreatedByAgrees = STRS.forall(c => STRS.forall(caller => plantedCreatedBy(log, c, caller) == oldCreatedBy(log, c, caller)))
}

// the same equalities in every state the models reach (sampled by
// rules_test.go): the store's side and the clients'
module rulesStore {
  import counters.* from "../model/counters"
  import rulesOld.*
  val agree = agreeOn(log)
}

module rulesCalls {
  import countersCalls.* from "../model/counters"
  import rulesOld.*
  val agree = agreeOn(log)
}
