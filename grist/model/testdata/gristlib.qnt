// grist.qnt's defs checked on small values (quint test), and a small
// machine over them (quint run): a log appended to by conditioned
// commands and an at-least-once processor, whose invariants restate what
// the defs promise. grist/model/model_test.go runs both.
module gristlib {
  import grist.* from "../grist"

  pure def ev(typ: str, tag: str, id: str): Event =
    { typ: typ, strs: [tag], ints: [], tags: Set(("k", tag)), cause: ("command", id) }

  run readsOfTest = assert(readsOf([("a", "k", "x"), ("b", "", "")]) == [{ typ: "a", tags: Set(("k", "x")) }, { typ: "b", tags: Set() }])

  run matchesTest = all {
    assert(matches(ev("a", "x", "c1"), { typ: "a", tags: Set() })),
    assert(matches(ev("a", "x", "c1"), { typ: "a", tags: Set(("k", "x")) })),
    assert(not(matches(ev("a", "x", "c1"), { typ: "a", tags: Set(("k", "y")) }))),
    assert(not(matches(ev("a", "x", "c1"), { typ: "b", tags: Set() }))),
  }

  run coversTest = all {
    // a type-only read covers a tagged need; a read of another tag does not
    assert(covers([{ typ: "a", tags: Set() }], { typ: "a", tags: Set(("k", "x")) })),
    assert(covers([{ typ: "a", tags: Set(("k", "x")) }], { typ: "a", tags: Set(("k", "x")) })),
    assert(not(covers([{ typ: "a", tags: Set(("k", "y")) }], { typ: "a", tags: Set(("k", "x")) }))),
    assert(not(covers([], { typ: "a", tags: Set() }))),
  }

  run conditionHoldsTest = {
    val log = [ev("a", "x", "c1"), ev("a", "y", "c2")]
    all {
      assert(conditionHolds(log, 1, [{ typ: "a", tags: Set(("k", "x")) }])),
      assert(not(conditionHolds(log, 1, [{ typ: "a", tags: Set(("k", "y")) }]))),
      assert(conditionHolds(log, 2, [{ typ: "a", tags: Set() }])),
      // a read position past the head, or before the log, is no read
      assert(not(conditionHolds(log, 3, []))),
      assert(not(conditionHolds(log, -1, []))),
    }
  }

  run nextOfTest = {
    val log = [ev("a", "x", "c1"), ev("b", "y", "c2"), ev("a", "z", "c3")]
    all { assert(nextOf(log, 0, "a") == 1), assert(nextOf(log, 1, "a") == 3), assert(nextOf(log, 3, "a") == 0) }
  }

  run batchIsTest = assert(batchIs([ev("a", "x", "c1"), failed("p", 2)], ["a", "grist.failed"], ["x", "p"], [2],
    [("command", "c1"), ("processor", "p")]))

  run keyStableTest = {
    val effects = [("p", 1, "p/t1/1", true), ("p", 2, "p/t1/2", false)]
    all {
      assert(keyStable(effects, "p", 1, "p/t1/1")),
      assert(not(keyStable(effects, "p", 1, "p/t1/2"))), // another trigger's key
      assert(not(keyStable(effects, "p", 3, "p/t1/1"))),
      assert(keyStable(effects, "p", 3, "p/t1/3")),
    }
  }

  run atLeastOnceTest = {
    val okThenFailed = [("p", 1, "k", true), ("p", 1, "k", false), ("p", 1, "k", false), ("p", 1, "k", false)]
    all {
      // ok, a crash, then EFFECT_TRIES failures: either outcome is admitted
      assert(atLeastOnceDone(okThenFailed, "p", 1, true, EFFECT_TRIES)),
      assert(atLeastOnceDone(okThenFailed, "p", 1, false, EFFECT_TRIES)),
      assert(not(atLeastOnceDone([("p", 1, "k", false)], "p", 1, false, EFFECT_TRIES))),
      assert(not(atLeastOnceDone([], "p", 1, true, EFFECT_TRIES))),
      assert(atLeastOnceOutcome("p", 1, false, []) == [failed("p", 1)]),
    }
  }
}

// A small machine over the defs: commands append a tagged event only
// when no event of their tag exists, conditioned on their read; a
// processor's effect runs at least once before its checkpoint.
module gristrun {
  import grist.* from "../grist"

  var log: List[Event]
  var answered: Set[str]
  var effects: List[Effect]
  var checkpoint: int

  action init = all { log' = [], answered' = Set(), effects' = [], checkpoint' = 0 }

  action create(tag: str, id: str, readPos: int): bool = {
    val reads = [{ typ: "a", tags: Set(("k", tag)) }]
    all {
      freshKey(answered, id),
      conditionHolds(log, readPos, reads),
      not(existsTagged(log, "a", "k", tag)),
      log' = log.append({ typ: "a", strs: [tag], ints: [], tags: Set(("k", tag)), cause: ("command", id) }),
      answered' = answered.union(Set(id)),
      effects' = effects,
      checkpoint' = checkpoint,
    }
  }

  action effect(ok: bool): bool = {
    val t = nextOf(log, checkpoint, "a")
    all {
      t > 0,
      keyStable(effects, "p", t, if (t == 1) "k1" else if (t == 2) "k2" else "k3"),
      effects' = effects.append(("p", t, if (t == 1) "k1" else if (t == 2) "k2" else "k3", ok)),
      log' = log, answered' = answered, checkpoint' = checkpoint,
    }
  }

  action done(ok: bool): bool = {
    val t = nextOf(log, checkpoint, "a")
    all {
      t > 0,
      atLeastOnceDone(effects, "p", t, ok, EFFECT_TRIES),
      log' = log.concat(atLeastOnceOutcome("p", t, ok, [])),
      checkpoint' = t,
      answered' = answered, effects' = effects,
    }
  }

  action step = {
    nondet tag = Set("x", "y").oneOf()
    nondet id = Set("c1", "c2", "c3").oneOf()
    nondet readPos = 0.to(log.length()).oneOf()
    nondet ok = Bool.oneOf()
    any { create(tag, id, readPos), effect(ok), done(ok) }
  }

  // every tag created once, every id appended once
  val unique = log.indices().forall(i => log.indices().forall(j =>
    (i < j and log[i].typ == "a" and log[j].typ == "a") implies (log[i].strs != log[j].strs and log[i].cause != log[j].cause)))
  // a checkpointed trigger had an effect
  val effected = 1.to(checkpoint).forall(t => t > log.length() or log[t - 1].typ != "a" or effects.indices().exists(i => effects[i]._2 == t))
}
