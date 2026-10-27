// examples/counters, grain's counter example on grist, as one tenant's
// log (docs/grist.md §8.1): the rules and invariants grist's generated
// skeleton (counters_gen.qnt, `counters model`: the events, the actions
// of every block, the frame conditions, step) calls. Read models are
// defs over the log, not variables: the model cannot disagree with
// itself about what a read model holds.

// The rules: each command's outcome, events and the reads it needs, the
// query's answer, the processors' events, the welcome's request of the
// mailer and the payout's of the bank, and the read models' folds.
module countersRules {
  import grist.* from "./grist"
  import countersEvents.* from "./counters_gen"

  pure val STRS: Set[str] = Set("a", "b")
  pure val INTS: Set[int] = 1.to(2)

  // totals, keyed by counter: the sum of its increments
  pure def total(log: List[Event], counter: str): int =
    totalsEvents(log, counter).foldl(0, (s, e) => s + counterIncrementedBy(e))
  // counter-names: each name's counter (a later create of a name wins)
  pure def counterNames(log: List[Event]): str -> str =
    counterNamesEvents(log).foldl(Map(), (m, e) => m.put(counterCreatedName(e), counterCreatedCounter(e)))
  // how many names name counter
  pure def namesOf(log: List[Event], counter: str): int =
    val names = counterNames(log)
    names.keys().filter(n => names.get(n) == counter).size()
  // actor created counter under some name: the one actor who may retire it
  pure def createdBy(log: List[Event], counter: str, actor: str): bool =
    log.select(e => e.typ == "counter-created" and counterCreatedCounter(e) == counter and counterCreatedCreator(e) == actor).length() > 0
  pure def isCounter(log: List[Event], counter: str): bool = existsTagged(log, "counter-created", "counter", counter)

  // create-counter: refused when a counter has the name, which its read
  // set must cover; the creator is the actor that sent it
  pure def createCounterOutcome(log: List[Event], c: CreateCounterArgs): str =
    if (existsTagged(log, "counter-created", "name", c.name)) "name-taken" else "ok"
  pure def createCounterEvents(log: List[Event], c: CreateCounterArgs): List[Event] = [counterCreated(c.counter, c.name, c.principal)]
  pure def createCounterNeeds(c: CreateCounterArgs, result: str): Set[Read] = Set(counterCreatedByName(c.name))

  // increment-counter: refused for a counter never created
  pure def incrementCounterOutcome(log: List[Event], c: IncrementCounterArgs): str =
    if (isCounter(log, c.counter)) "ok" else "unknown-counter"
  pure def incrementCounterEvents(log: List[Event], c: IncrementCounterArgs): List[Event] = [counterIncremented(c.counter, c.by)]
  pure def incrementCounterNeeds(c: IncrementCounterArgs, result: str): Set[Read] = Set(counterCreatedByCounter(c.counter))

  // retire-counter: its creator's only (principal: the step's actor), its
  // total as of the commit
  pure def retireCounterOutcome(log: List[Event], c: RetireCounterArgs): str =
    if (not(isCounter(log, c.counter))) "unknown-counter" else if (createdBy(log, c.counter, c.principal)) "ok" else "not-creator"
  pure def retireCounterEvents(log: List[Event], c: RetireCounterArgs): List[Event] = [counterRetired(c.counter, total(log, c.counter))]
  pure def retireCounterNeeds(c: RetireCounterArgs, result: str): Set[Read] = Set(counterCreatedByCounter(c.counter))

  pure def showCounterAnswer(log: List[Event], q: ShowCounterArgs): ShowCounterResult =
    { total: total(log, q.counter), names: namesOf(log, q.counter) }

  // average: the tenant's sum and count of increments as of the trigger
  pure def averageEmits(prefix: List[Event], trigger: int): List[Event] =
    val incs = prefix.select(e => e.typ == "counter-incremented")
    List(averageComputed(incs.foldl(0, (s, e) => s + counterIncrementedBy(e)), incs.length()))
  pure def welcomeSuccess(prefix: List[Event], trigger: int): List[Event] = [notified(counterCreatedCounter(prefix[trigger - 1]))]
  // a creation's welcome mails the counter and its name (the mailer sends
  // every mail: Send is not Keyed, and the generated mailerSendAdmits
  // admits any)
  pure def welcomeMailerSend(prefix: List[Event], trigger: int): MailerSendRequest =
    { counter: counterCreatedCounter(prefix[trigger - 1]), name: counterCreatedName(prefix[trigger - 1]) }
  // welcome and payout skip no trigger (their handlers never Skip)
  pure def welcomeSkips(prefix: List[Event], trigger: int): bool = false
  // a retirement's payout names the retirement, so each one's outcome is its own
  pure def payoutSuccess(prefix: List[Event], trigger: int): List[Event] = [paidOut(counterRetiredCounter(prefix[trigger - 1]), trigger)]
  pure def payoutSkips(prefix: List[Event], trigger: int): bool = false
  // a retirement's payout pays the retired counter its total, as of the
  // retirement (the bank applies each key once: Pay is Keyed, and the
  // generated bankPayAdmits says so)
  pure def payoutBankPay(prefix: List[Event], trigger: int): BankPayRequest =
    { counter: counterRetiredCounter(prefix[trigger - 1]), amount: counterRetiredTotal(prefix[trigger - 1]) }

  // the design's: every created name is created once (as many distinct
  // names as creates: linear in the log)
  pure def namesUnique(log: List[Event]): bool =
    val creates = log.select(e => e.typ == "counter-created")
    creates.foldl(Set(), (s, e) => s.union(Set(counterCreatedName(e)))).size() == creates.length()
}

// The store's side (counters.binding.yaml and its variants).
module counters {
  import grist.* from "./grist"
  import countersEvents.* from "./counters_gen"
  import countersRules.*
  import countersGen.* from "./counters_gen"
  export countersGen.*

  // grist: invariant default retiring real
  val uniqueNames = namesUnique(log)

  // each average-computed is the sum and count of the increments through
  // its trigger: the k-th is the k-th increment's prefix
  // grist: invariant default retiring real
  val averageAsOfTrigger =
    val sums = log.foldl([], (acc, e) =>
      if (e.typ == "counter-incremented") acc.append(if (acc.length() == 0) e.ints[0] else acc[acc.length() - 1] + e.ints[0])
      else acc)
    val avgs = log.select(e => e.typ == "average-computed")
    avgs.indices().forall(k => k < sums.length() and avgs[k].ints == [sums[k], k + 1])

  // every created counter was welcomed, or its welcome failed every try
  // and says so
  // grist: eventually default retiring real
  val everyCounterWelcomedOrFailed =
    log.indices().forall(i => log[i].typ == "counter-created" implies and {
      checkpoints.get("welcome") >= i + 1,
      effectsOf(effects, "welcome", i + 1, true) > 0 or holds(log, failed("welcome", i + 1)),
    })

  // every retirement's payout was checkpointed (at most once: its payment
  // and its outcome may be lost)
  // grist: eventually retiring real
  val everyRetirementHandled =
    log.indices().forall(i => log[i].typ == "counter-retired" implies checkpoints.get("payout") >= i + 1)

  // Site claims: every answer a caller got is one its dependency gave
  // (its served record of the call, request, outcome and response alike,
  // a replay's being an earlier one's), or unknown. They need the
  // dependency's site recorded: in every simulation (gristsim serves
  // both), and on the real tier for the bank only, which runs behind
  // grist.ServeCalls with its own step file; the real tier's mailer is a
  // third party's, which records nothing, so the real variant
  // (counters.real.binding.yaml) leaves the mailer's claim out, as
  // docs/grist-instrumentation.md §5's decision 4 has it.
  // grist: eventually default retiring real
  val everyPaymentAnswerServed =
    bankPayCalls.indices().forall(i => bankPayCalls[i].result != "unknown" implies
      bankPayRecords.indices().exists(j => bankPayRecords[j] == bankPayCalls[i]))
  // grist: eventually default retiring
  val everyMailAnswerServed =
    mailerSendCalls.indices().forall(i => mailerSendCalls[i].result != "unknown" implies
      mailerSendRecords.indices().exists(j => mailerSendRecords[j] == mailerSendCalls[i]))

  // test-only: every retirement paid out exactly once, by the bank's own
  // record of its payments (its served records, each naming its trigger).
  // At most once allows none, by right (a crash between the checkpoint
  // and the payment); an application that must pay needs another
  // guarantee.
  // grist: eventually exactly
  val everyRetirementPaidOnce =
    log.indices().forall(i => log[i].typ == "counter-retired" implies
      bankPayRecords.select(p => p.trigger == i + 1 and p.result == "ok").length() == 1)

  // test-only: every payment step's outcome is in the log (paid-out after
  // an ok one, grist.failed or grist.unknown after a failed one): a crash
  // after the payment loses it, by right; a runtime or an app that never
  // appends it conforms to the actions (the outcome is optional)
  // grist: eventually exactly
  val everyPayoutOutcomeLogged =
    atMostOnceOutcomesLogged(log, effects, "payout", t => if (t > 0 and t <= log.length()) payoutSucceeded(log, t) else [])

  // test-only: one ok welcome per counter. At least once allows more, by
  // right; an application whose effect must not repeat states it so
  // grist: violable once
  val welcomedOnce = log.indices().forall(i => effectsOf(effects, "welcome", i + 1, true) <= 1)
}

// The clients' side (counters.calls.binding.yaml), linearized.
module countersCalls {
  import grist.* from "./grist"
  import countersEvents.* from "./counters_gen"
  import countersRules.*
  import countersCallsGen.* from "./counters_gen"
  export countersCallsGen.*

  // grist: invariant
  val uniqueNames = namesUnique(log)
}
