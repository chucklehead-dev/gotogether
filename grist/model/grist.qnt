// grist's guarantees, in one tenant's state (docs/grist.md §3.3): the
// first, hand-written version, slice 1's. An application's model imports
// it (vendored into its model/, as examples/twophase vendors choreo.qnt)
// and keeps its state as one tenant's log; each tenant's trace is its own
// instance of the model from init. grist's own tests typecheck it and run
// it (grist/model/model_test.go, testdata/gristlib.qnt).
//
// An event is its type, its string and integer fields in the Go struct's
// order (what a step records as obs.strs and obs.ints), its tags and its
// cause, a (kind, name) pair: ("command", the command id) or
// ("processor", the processor's name) (obs.causes). A read is a type and
// the tags it requires; slice 1 records a read set as (type, tag key, tag
// value) triples, key "" for a type-only read, which readsOf turns into
// Reads (slice 2's typed template function records Reads directly).
module grist {
  type Cause = (str, str)
  type Event = { typ: str, strs: List[str], ints: List[int], tags: Set[(str, str)], cause: Cause }
  type Read = { typ: str, tags: Set[(str, str)] }
  // an effect step: (processor, trigger, key, ok)
  type Effect = (str, int, str, bool)

  // a recorded read set (slice 1's triples) as Reads
  pure def readsOf(triples: List[(str, str, str)]): List[Read] =
    triples.foldl([], (acc, t) => acc.append({ typ: t._1, tags: if (t._2 == "") Set() else Set((t._2, t._3)) }))

  // e matches the read q
  pure def matches(e: Event, q: Read): bool =
    e.typ == q.typ and q.tags.subseteq(e.tags)

  // The read set covers what a decision needs to know: some read selects
  // every event need selects (need's type, a subset of its tags). An
  // application's action requires it of the reads its rule depends on, so
  // a runtime that loses a read from the read set (and so from the
  // condition) is caught at every commit, not only when a race hits it.
  pure def covers(reads: List[Read], need: Read): bool =
    reads.indices().exists(j => reads[j].typ == need.typ and reads[j].tags.subseteq(need.tags))

  // Conditional append: the decision read through readPos, at most the
  // head, and no event after readPos matches a read of the set. Part of
  // every conditioned commit's precondition, a refusal's included, with
  // the read set the step recorded.
  pure def conditionHolds(log: List[Event], readPos: int, reads: List[Read]): bool = and {
    0 <= readPos,
    readPos <= log.length(),
    log.indices().forall(i => i < readPos or reads.indices().forall(j => not(matches(log[i], reads[j])))),
  }

  // an event of typ tagged key=value exists in log
  pure def existsTagged(log: List[Event], typ: str, key: str, value: str): bool =
    log.indices().exists(i => matches(log[i], { typ: typ, tags: Set((key, value)) }))

  // A key names at most one outcome: answered are the command ids answered so
  // far, by a commit or a committed refusal (a rejection, a conflict). A
  // command's every committed step requires its id fresh and adds it.
  pure def freshKey(answered: Set[str], id: str): bool = not(answered.contains(id))

  // the position (from 1) of the first event of typ after position from,
  // 0 if there is none: a processor's next trigger
  pure def nextOf(log: List[Event], from: int, typ: str): int =
    val after = log.indices().filter(i => i >= from and log[i].typ == typ)
    if (after.size() == 0) 0 else after.fold(log.length(), (m, i) => if (i < m) i else m) + 1

  // a batch as a step records it: its types, string fields, integer
  // fields and causes
  pure def batchIs(b: List[Event], types: List[str], strs: List[str], ints: List[int], causes: List[Cause]): bool = and {
    b.foldl([], (acc, e) => acc.append(e.typ)) == types,
    b.foldl([], (acc, e) => acc.concat(e.strs)) == strs,
    b.foldl([], (acc, e) => acc.concat(e.ints)) == ints,
    b.foldl([], (acc, e) => acc.append(e.cause)) == causes,
  }

  // the event a processor appends when a trigger's handling failed every try
  pure def failed(processor: str, trigger: int): Event =
    { typ: "grist.failed", strs: [processor], ints: [trigger], tags: Set(("processor", processor)), cause: ("processor", processor) }

  // the effect steps of processor for trigger, ok or not
  pure def effectsOf(effects: List[Effect], processor: str, trigger: int, ok: bool): int =
    effects.foldl(0, (n, e) => if (e._1 == processor and e._2 == trigger and e._4 == ok) n + 1 else n)

  // An effect's key is the trigger's: every effect step of processor for
  // trigger carries key, and no other trigger's does (the same key on
  // every run, so a dependency that dedupes by key can make "at least
  // once" "once").
  pure def keyStable(effects: List[Effect], processor: str, trigger: int, key: str): bool =
    effects.indices().forall(i => effects[i]._1 == processor implies ((effects[i]._2 == trigger) == (effects[i]._3 == key)))

  // At least once, the checkpoint commits the outcome its run reached
  // (ok: the step's record of it): success after an ok effect step, a
  // grist.failed after tries failed ones. A run may have both (ok, a crash
  // before the checkpoint, then tries failures on the restarted node):
  // the committed run decides, and the model admits either.
  pure def atLeastOnceDone(effects: List[Effect], processor: str, trigger: int, ok: bool, tries: int): bool =
    if (ok) effectsOf(effects, processor, trigger, true) > 0
    else effectsOf(effects, processor, trigger, false) >= tries

  // the events an at-least-once checkpoint appends: success's, or the
  // trigger's grist.failed
  pure def atLeastOnceOutcome(processor: str, trigger: int, ok: bool, success: List[Event]): List[Event] =
    if (ok) success else [failed(processor, trigger)]

  // grist's default EffectTries: a failed effect is checkpointed with
  // grist.failed after this many failures
  pure val EFFECT_TRIES = 3
}
