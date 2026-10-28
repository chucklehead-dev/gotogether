// examples/counters' rules (docs/grist.md §8.1): the holes grist's
// generated skeleton (counters_steps.qnt, `counters model`) calls. Read
// models are defs over the log, not variables: the model cannot disagree
// with itself about what a read model holds.

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
  // caller created counter under some name: the one caller who may retire it
  pure def createdBy(log: List[Event], counter: str, caller: str): bool =
    log.select(e => e.typ == "counter-created" and counterCreatedCounter(e) == counter and counterCreatedCreator(e) == caller).length() > 0
  pure def isCounter(log: List[Event], counter: str): bool = existsTagged(log, "counter-created", "counter", counter)

  // create-counter: refused when a counter has the name, which its read
  // set must cover; the creator is the caller that sent it
  pure def createCounterOutcome(log: List[Event], c: CreateCounterArgs): str =
    if (existsTagged(log, "counter-created", "name", c.name)) "name-taken" else "ok"
  pure def createCounterEvents(log: List[Event], c: CreateCounterArgs): List[Event] = [counterCreated(c.counter, c.name, c.caller)]
  pure def createCounterNeeds(c: CreateCounterArgs, result: str): Set[Read] = Set(counterCreatedByName(c.name))

  // increment-counter: refused for a counter never created
  pure def incrementCounterOutcome(log: List[Event], c: IncrementCounterArgs): str =
    if (isCounter(log, c.counter)) "ok" else "unknown-counter"
  pure def incrementCounterEvents(log: List[Event], c: IncrementCounterArgs): List[Event] = [counterIncremented(c.counter, c.by)]
  pure def incrementCounterNeeds(c: IncrementCounterArgs, result: str): Set[Read] = Set(counterCreatedByCounter(c.counter))

  // retire-counter: its creator's only (the step's caller), its
  // total as of the commit
  pure def retireCounterOutcome(log: List[Event], c: RetireCounterArgs): str =
    if (not(isCounter(log, c.counter))) "unknown-counter" else if (createdBy(log, c.counter, c.caller)) "ok" else "not-creator"
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
