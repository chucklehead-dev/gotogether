// grist's guarantees, in one tenant's state (docs/grist.md §3.3): the
// first, hand-written version, slice 1's. An application's model imports
// it (vendored into its model/, as examples/twophase vendors choreo.qnt)
// and keeps its state as one tenant's log; each tenant's trace is its own
// instance of the model from init.
//
// An event is its type, its string and integer fields in the Go struct's
// order (what a step records as obs.strs and obs.ints), its tags and its
// cause (the command id that appended it; "" for a processor's). A read
// set is a list of (type, tag key, tag value) triples, key "" for a
// type-only query (a step's reads).
module grist {
  type Event = { typ: str, strs: List[str], ints: List[int], tags: Set[(str, str)], cause: str }
  type Read = (str, str, str)

  // e matches the query q
  pure def matches(e: Event, q: Read): bool =
    e.typ == q._1 and (q._2 == "" or e.tags.contains((q._2, q._3)))

  // Conditional append: no event after readPos matches a query of the
  // read set. Part of every conditioned commit's precondition, with the
  // read set the step recorded.
  pure def conditionHolds(log: List[Event], readPos: int, reads: List[Read]): bool = and {
    readPos <= log.length(),
    log.indices().forall(i => i < readPos or reads.indices().forall(j => not(matches(log[i], reads[j])))),
  }

  // an event of typ tagged key=value exists in log
  pure def existsTagged(log: List[Event], typ: str, key: str, value: str): bool =
    log.indices().exists(i => matches(log[i], (typ, key, value)))

  // no event of log was appended by command id: a key names at most one
  // committed outcome
  pure def freshCause(log: List[Event], id: str): bool =
    log.indices().forall(i => log[i].cause != id)

  // the position (from 1) of the first event of typ after position from,
  // 0 if there is none: a processor's next trigger
  pure def nextOf(log: List[Event], from: int, typ: str): int =
    val after = log.indices().filter(i => i >= from and log[i].typ == typ)
    if (after.size() == 0) 0 else after.fold(log.length(), (m, i) => if (i < m) i else m) + 1

  // a batch as a step records it: its types, string fields, integer fields
  pure def batchIs(b: List[Event], types: List[str], strs: List[str], ints: List[int]): bool = and {
    b.foldl([], (acc, e) => acc.append(e.typ)) == types,
    b.foldl([], (acc, e) => acc.concat(e.strs)) == strs,
    b.foldl([], (acc, e) => acc.concat(e.ints)) == ints,
  }

  // the event a processor appends when a trigger's handling failed every try
  pure def failed(processor: str, trigger: int): Event =
    { typ: "grist.failed", strs: [processor], ints: [trigger], tags: Set(("processor", processor)), cause: "" }

  // the effect steps of processor for trigger, ok or not
  pure def effectsOf(effects: List[(str, int, bool)], processor: str, trigger: int, ok: bool): int =
    effects.foldl(0, (n, e) => if (e._1 == processor and e._2 == trigger and e._3 == ok) n + 1 else n)

  // grist's EffectTries: a failed effect is checkpointed with grist.failed
  // after this many failures
  pure val EFFECT_TRIES = 3
}
