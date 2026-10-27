// examples/counters, grain's counter example on grist, as one tenant's
// log (docs/grist.md §8.1). Read models are defs over the log, not
// variables: the model cannot disagree with itself about what a read
// model holds. Hand-written in slice 1 (slice 2 generates the skeleton).

// The events and read models, shared by the commit-side model (counters)
// and the client-side one (countersCalls).
module countersLib {
  import grist.* from "./grist"

  pure def created(counter: str, name: str, id: str): Event =
    { typ: "counter-created", strs: [counter, name], ints: [], tags: Set(("counter", counter), ("name", name)), cause: id }
  pure def incremented(counter: str, by: int, id: str): Event =
    { typ: "counter-incremented", strs: [counter], ints: [by], tags: Set(("counter", counter)), cause: id }
  pure def averageComputed(sum: int, count: int): Event =
    { typ: "average-computed", strs: [], ints: [sum, count], tags: Set(), cause: "" }
  pure def notified(counter: str): Event =
    { typ: "notified", strs: [counter], ints: [], tags: Set(("counter", counter)), cause: "" }

  // totals, keyed by counter: the sum of its increments
  pure def total(log: List[Event], counter: str): int =
    log.foldl(0, (s, e) => if (e.typ == "counter-incremented" and e.strs[0] == counter) s + e.ints[0] else s)
  // counter-names: each name's counter (a later create of a name wins)
  pure def counterNames(log: List[Event]): str -> str =
    log.foldl(Map(), (m, e) => if (e.typ == "counter-created") m.put(e.strs[1], e.strs[0]) else m)
  // how many names name counter
  pure def namesOf(log: List[Event], counter: str): int =
    val names = counterNames(log)
    names.keys().filter(n => names.get(n) == counter).size()

  // the design's: every created name is created once
  pure def namesUnique(log: List[Event]): bool =
    log.indices().forall(i => log.indices().forall(j =>
      (i < j and log[i].typ == "counter-created" and log[j].typ == "counter-created") implies log[i].strs[1] != log[j].strs[1]))
}

// The commit side: every step recorded where it took effect (gristsim's
// AtCommits).
module counters {
  import grist.* from "./grist"
  import countersLib.*
  // the bindings' expects and generated modules read their defs here
  export grist.*
  export countersLib.*

  var log: List[Event]
  // each processor's checkpoint: the last trigger it handled
  var checkpoints: str -> int
  // the batch the last step appended (what its observations are checked against)
  var appended: List[Event]
  // the effect steps: (processor, trigger, ok)
  var effects: List[(str, int, bool)]

  action init = all {
    log' = [],
    checkpoints' = Map("average" -> 0, "welcome" -> 0),
    appended' = [],
    effects' = [],
  }

  action appends(b: List[Event]): bool = all {
    log' = log.concat(b),
    appended' = b,
    checkpoints' = checkpoints,
    effects' = effects,
  }

  // a refusal: it read through readPos, which is the log's length where it
  // is ordered (2·readPos+1), and changes nothing
  action refused(readPos: int): bool = all {
    log.length() == readPos,
    log' = log,
    appended' = [],
    checkpoints' = checkpoints,
    effects' = effects,
  }

  action createCounter(counter: str, name: str, id: str, readPos: int, reads: List[Read], result: str): bool =
    if (result == "ok") all {
      not(existsTagged(log, "counter-created", "name", name)),
      conditionHolds(log, readPos, reads),
      freshCause(log, id),
      appends([created(counter, name, id)]),
    } else if (result == "name-taken") all {
      existsTagged(log, "counter-created", "name", name),
      refused(readPos),
    } else all { result == "conflict", refused(readPos) }

  action incrementCounter(counter: str, by: int, id: str, readPos: int, reads: List[Read], result: str): bool =
    if (result == "ok") all {
      existsTagged(log, "counter-created", "counter", counter),
      conditionHolds(log, readPos, reads),
      freshCause(log, id),
      appends([incremented(counter, by, id)]),
    } else if (result == "unknown-counter") all {
      not(existsTagged(log, "counter-created", "counter", counter)),
      refused(readPos),
    } else all { result == "conflict", refused(readPos) }

  // a query: it reads through readPos, changes nothing; its answer is
  // checked against total and namesOf (the binding's expect)
  action showCounter(counter: str, readPos: int): bool = refused(readPos)

  // average: on each increment, the tenant's sum and count as of the
  // trigger (the handler reads log.slice(0, trigger), whatever came after)
  action average(trigger: int): bool = all {
    trigger > 0,
    trigger == nextOf(log, checkpoints.get("average"), "counter-incremented"),
    val incs = log.slice(0, trigger).select(e => e.typ == "counter-incremented")
    val avg = averageComputed(incs.foldl(0, (s, e) => s + e.ints[0]), incs.length())
    all {
      log' = log.append(avg),
      appended' = [avg],
      checkpoints' = checkpoints.set("average", trigger),
      effects' = effects,
    },
  }

  // welcome's effect (the mailer's send), at least once: any number of
  // tries for the next created counter before its checkpoint
  action welcomeEffect(trigger: int, key: str, ok: bool): bool = all {
    trigger > 0,
    trigger == nextOf(log, checkpoints.get("welcome"), "counter-created"),
    log' = log,
    appended' = [],
    checkpoints' = checkpoints,
    effects' = effects.append(("welcome", trigger, ok)),
  }

  // welcome's checkpoint, only after an effect step: notified once the
  // effect succeeded, grist.failed after EFFECT_TRIES failures
  action welcome(trigger: int): bool = all {
    trigger > 0,
    trigger == nextOf(log, checkpoints.get("welcome"), "counter-created"),
    val outcome =
      if (effectsOf(effects, "welcome", trigger, true) > 0) [notified(log[trigger - 1].strs[0])]
      else if (effectsOf(effects, "welcome", trigger, false) >= EFFECT_TRIES) [failed("welcome", trigger)]
      else []
    all {
      outcome.length() > 0,
      log' = log.concat(outcome),
      appended' = outcome,
      checkpoints' = checkpoints.set("welcome", trigger),
      effects' = effects,
    },
  }

  // the model on its own (gotogether simulate)
  action step = {
    nondet counter = Set("a", "b").oneOf()
    nondet name = Set("a", "b").oneOf()
    nondet by = 1.to(2).oneOf()
    nondet id = Set("i1", "i2", "i3", "i4").oneOf()
    nondet ok = Bool.oneOf()
    any {
      createCounter(counter, name, id, log.length(), [("counter-created", "name", name)], "ok"),
      createCounter(counter, name, id, log.length(), [("counter-created", "name", name)], "name-taken"),
      incrementCounter(counter, by, id, log.length(),
        [("counter-created", "counter", counter), ("counter-incremented", "counter", counter)], "ok"),
      incrementCounter(counter, by, id, log.length(), [("counter-created", "counter", counter)], "unknown-counter"),
      showCounter(counter, log.length()),
      average(nextOf(log, checkpoints.get("average"), "counter-incremented")),
      welcomeEffect(nextOf(log, checkpoints.get("welcome"), "counter-created"), "key", ok),
      welcome(nextOf(log, checkpoints.get("welcome"), "counter-created")),
    }
  }

  // the design's: every created name is created once
  val uniqueNames = namesUnique(log)

  // each average-computed is the sum and count of the increments through
  // its trigger: the k-th is the k-th increment's prefix
  val averageAsOfTrigger =
    val sums = log.foldl([], (acc, e) =>
      if (e.typ == "counter-incremented") acc.append(if (acc.length() == 0) e.ints[0] else acc[acc.length() - 1] + e.ints[0])
      else acc)
    val avgs = log.select(e => e.typ == "average-computed")
    avgs.indices().forall(k => k < sums.length() and avgs[k].ints == [sums[k], k + 1])

  // eventually (after a settle): every created counter was welcomed, or
  // its welcome failed every try and says so
  val everyCounterWelcomedOrFailed =
    log.indices().forall(i => log[i].typ == "counter-created" implies and {
      checkpoints.get("welcome") >= i + 1,
      effectsOf(effects, "welcome", i + 1, true) > 0 or log.indices().exists(j => log[j] == failed("welcome", i + 1)),
    })

  // test-only (counters.once.binding.yaml): one ok welcome per counter.
  // At least once allows more, by right; an application whose effect
  // must not repeat states it so.
  val welcomedOnce =
    log.indices().forall(i => effectsOf(effects, "welcome", i + 1, true) <= 1)
}

// The client side: every command and query recorded where a client saw
// it (gristsim's AtClients), linearized by qtest.Linearize. No positions,
// no processors: the log holds the commands' events alone.
module countersCalls {
  import grist.* from "./grist"
  import countersLib.*
  export grist.*
  export countersLib.*

  var log: List[Event]

  action init = log' = []

  action createCounter(counter: str, name: str, id: str, result: str): bool =
    if (result == "ok") all {
      not(existsTagged(log, "counter-created", "name", name)),
      freshCause(log, id),
      log' = log.append(created(counter, name, id)),
    } else if (result == "name-taken") all {
      existsTagged(log, "counter-created", "name", name),
      log' = log,
    } else all { result == "conflict", log' = log }

  action incrementCounter(counter: str, by: int, id: str, result: str): bool =
    if (result == "ok") all {
      existsTagged(log, "counter-created", "counter", counter),
      freshCause(log, id),
      log' = log.append(incremented(counter, by, id)),
    } else if (result == "unknown-counter") all {
      not(existsTagged(log, "counter-created", "counter", counter)),
      log' = log,
    } else all { result == "conflict", log' = log }

  action showCounter(counter: str): bool = log' = log

  action step = {
    nondet counter = Set("a", "b").oneOf()
    nondet name = Set("a", "b").oneOf()
    nondet by = 1.to(2).oneOf()
    nondet id = Set("i1", "i2", "i3").oneOf()
    any {
      createCounter(counter, name, id, "ok"),
      createCounter(counter, name, id, "name-taken"),
      incrementCounter(counter, by, id, "ok"),
      incrementCounter(counter, by, id, "unknown-counter"),
      showCounter(counter),
    }
  }

  val uniqueNames = namesUnique(log)
}
