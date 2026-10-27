// examples/counters, grain's counter example on grist, as one tenant's
// log (docs/grist.md §8.1). Read models are defs over the log, not
// variables: the model cannot disagree with itself about what a read
// model holds. Hand-written in slice 1 (slice 2 generates the skeleton).

// The events and read models, shared by the commit-side model (counters)
// and the client-side one (countersCalls).
module countersLib {
  import grist.* from "./grist"

  pure def created(counter: str, name: str, id: str): Event =
    { typ: "counter-created", strs: [counter, name], ints: [], tags: Set(("counter", counter), ("name", name)), cause: ("command", id) }
  pure def incremented(counter: str, by: int, id: str): Event =
    { typ: "counter-incremented", strs: [counter], ints: [by], tags: Set(("counter", counter)), cause: ("command", id) }
  pure def averageComputed(sum: int, count: int): Event =
    { typ: "average-computed", strs: [], ints: [sum, count], tags: Set(), cause: ("processor", "average") }
  pure def notified(counter: str): Event =
    { typ: "notified", strs: [counter], ints: [], tags: Set(("counter", counter)), cause: ("processor", "welcome") }

  // what each command's rule reads: whether a counter has the name, and
  // whether the counter exists. A committed step's read set must cover
  // it (grist's covers), whatever else the decision read
  pure def nameRead(name: str): Read = { typ: "counter-created", tags: Set(("name", name)) }
  pure def counterRead(counter: str): Read = { typ: "counter-created", tags: Set(("counter", counter)) }

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

  // the design's: every created name is created once (as many distinct
  // names as creates: linear in the log, where comparing every pair of
  // creates was quadratic)
  pure def namesUnique(log: List[Event]): bool =
    val creates = log.select(e => e.typ == "counter-created")
    creates.foldl(Set(), (s, e) => s.union(Set(e.strs[1]))).size() == creates.length()
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
  // the effect steps: (processor, trigger, key, ok)
  var effects: List[Effect]
  // the command ids answered so far, by a commit or a committed refusal
  var answered: Set[str]

  action init = all {
    log' = [],
    checkpoints' = Map("average" -> 0, "welcome" -> 0),
    appended' = [],
    effects' = [],
    answered' = Set(),
  }

  // a command's commit of b, answering id
  action appends(b: List[Event], id: str): bool = all {
    log' = log.concat(b),
    appended' = b,
    checkpoints' = checkpoints,
    effects' = effects,
    answered' = answered.union(Set(id)),
  }

  // a committed refusal: its marker answers id, nothing is appended. It
  // commits at the head, at or after the position it read through
  action refuses(readPos: int, id: str): bool = all {
    readPos <= log.length(),
    appends([], id),
  }

  // a step that commits nothing (a query, a reused key): it read through
  // readPos, which is the log's length where it is ordered (2·readPos+1)
  action readsThrough(readPos: int): bool = all {
    log.length() == readPos,
    log' = log,
    appended' = [],
    checkpoints' = checkpoints,
    effects' = effects,
    answered' = answered,
  }

  // A command's steps. Every committed outcome needs its id fresh and its
  // read set covering what its rule reads; an ok or a rejection needs its
  // condition to have held (nothing it read changed since readPos); a
  // conflict needs it not to have (the reads kept changing), and commits
  // unconditioned. A reused key commits nothing and needs the key taken.
  action createCounter(counter: str, name: str, id: str, readPos: int, reads: List[Read], result: str): bool =
    if (result == "idempotency-key-reused") all { answered.contains(id), readsThrough(readPos) }
    else if (result == "conflict") all {
      freshKey(answered, id),
      covers(reads, nameRead(name)),
      not(conditionHolds(log, readPos, reads)),
      refuses(readPos, id),
    } else all {
      freshKey(answered, id),
      covers(reads, nameRead(name)),
      conditionHolds(log, readPos, reads),
      if (result == "ok") all {
        not(existsTagged(log, "counter-created", "name", name)),
        appends([created(counter, name, id)], id),
      } else all {
        result == "name-taken",
        existsTagged(log, "counter-created", "name", name),
        refuses(readPos, id),
      },
    }

  action incrementCounter(counter: str, by: int, id: str, readPos: int, reads: List[Read], result: str): bool =
    if (result == "idempotency-key-reused") all { answered.contains(id), readsThrough(readPos) }
    else if (result == "conflict") all {
      freshKey(answered, id),
      covers(reads, counterRead(counter)),
      not(conditionHolds(log, readPos, reads)),
      refuses(readPos, id),
    } else all {
      freshKey(answered, id),
      covers(reads, counterRead(counter)),
      conditionHolds(log, readPos, reads),
      if (result == "ok") all {
        existsTagged(log, "counter-created", "counter", counter),
        appends([incremented(counter, by, id)], id),
      } else all {
        result == "unknown-counter",
        not(existsTagged(log, "counter-created", "counter", counter)),
        refuses(readPos, id),
      },
    }

  // a query: it reads through readPos, changes nothing; its answer is
  // checked against total and namesOf (the binding's expect)
  action showCounter(counter: str, readPos: int): bool = readsThrough(readPos)

  // the processors' steps change neither the commands' keys nor each other's
  action processes(b: List[Event], p: str, trigger: int): bool = all {
    log' = log.concat(b),
    appended' = b,
    checkpoints' = checkpoints.set(p, trigger),
    effects' = effects,
    answered' = answered,
  }

  // average: on each increment, the tenant's sum and count as of the
  // trigger (the handler reads log.slice(0, trigger), whatever came after)
  action average(trigger: int): bool = all {
    trigger > 0,
    trigger == nextOf(log, checkpoints.get("average"), "counter-incremented"),
    val incs = log.slice(0, trigger).select(e => e.typ == "counter-incremented")
    processes([averageComputed(incs.foldl(0, (s, e) => s + e.ints[0]), incs.length())], "average", trigger),
  }

  // welcome's effect (the mailer's send), at least once: any number of
  // tries for the next created counter before its checkpoint, every one
  // under the trigger's key
  action welcomeEffect(trigger: int, key: str, ok: bool): bool = all {
    trigger > 0,
    trigger == nextOf(log, checkpoints.get("welcome"), "counter-created"),
    keyStable(effects, "welcome", trigger, key),
    log' = log,
    appended' = [],
    checkpoints' = checkpoints,
    effects' = effects.append(("welcome", trigger, key, ok)),
    answered' = answered,
  }

  // welcome's checkpoint, only after effect steps: notified after an ok
  // one, grist.failed after EFFECT_TRIES failed ones, whichever the run
  // committed (ok: its record of which)
  action welcome(trigger: int, ok: bool): bool = all {
    trigger > 0,
    trigger == nextOf(log, checkpoints.get("welcome"), "counter-created"),
    atLeastOnceDone(effects, "welcome", trigger, ok, EFFECT_TRIES),
    processes(atLeastOnceOutcome("welcome", trigger, ok, [notified(log[trigger - 1].strs[0])]), "welcome", trigger),
  }

  // the model on its own (gotogether simulate)
  action step = {
    nondet counter = Set("a", "b").oneOf()
    nondet name = Set("a", "b").oneOf()
    nondet by = 1.to(2).oneOf()
    nondet id = Set("i1", "i2", "i3", "i4").oneOf()
    nondet key = Set("k1", "k2", "k3").oneOf()
    nondet ok = Bool.oneOf()
    any {
      createCounter(counter, name, id, log.length(), [nameRead(name)], "ok"),
      createCounter(counter, name, id, log.length(), [nameRead(name)], "name-taken"),
      incrementCounter(counter, by, id, log.length(), [counterRead(counter), { typ: "counter-incremented", tags: Set(("counter", counter)) }], "ok"),
      incrementCounter(counter, by, id, log.length(), [counterRead(counter)], "unknown-counter"),
      showCounter(counter, log.length()),
      average(nextOf(log, checkpoints.get("average"), "counter-incremented")),
      welcomeEffect(nextOf(log, checkpoints.get("welcome"), "counter-created"), key, ok),
      welcome(nextOf(log, checkpoints.get("welcome"), "counter-created"), ok),
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
// no processors: the log holds the commands' events alone. A key's first
// answer is final here too: a refused id never commits later.
module countersCalls {
  import grist.* from "./grist"
  import countersLib.*
  export grist.*
  export countersLib.*

  var log: List[Event]
  var answered: Set[str]

  action init = all { log' = [], answered' = Set() }

  action answers(b: List[Event], id: str): bool = all {
    freshKey(answered, id),
    log' = log.concat(b),
    answered' = answered.union(Set(id)),
  }

  action createCounter(counter: str, name: str, id: str, result: str): bool =
    if (result == "ok") all {
      not(existsTagged(log, "counter-created", "name", name)),
      answers([created(counter, name, id)], id),
    } else if (result == "name-taken") all {
      existsTagged(log, "counter-created", "name", name),
      answers([], id),
    } else all { result == "conflict", answers([], id) }

  action incrementCounter(counter: str, by: int, id: str, result: str): bool =
    if (result == "ok") all {
      existsTagged(log, "counter-created", "counter", counter),
      answers([incremented(counter, by, id)], id),
    } else if (result == "unknown-counter") all {
      not(existsTagged(log, "counter-created", "counter", counter)),
      answers([], id),
    } else all { result == "conflict", answers([], id) }

  action showCounter(counter: str): bool = all { log' = log, answered' = answered }

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
