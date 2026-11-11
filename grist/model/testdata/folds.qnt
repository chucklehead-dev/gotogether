// grist.qnt's folds against the index quantifiers and set folds they
// replaced (docs/performance.md, the grist library's folds): the earlier
// definitions, verbatim, and a machine that grows a log, effect steps and
// call keys of every shape the definitions read, whose invariant is that
// each new definition equals its earlier one at every argument of small
// ranges. grist/model/model_test.go samples it (quint run); quint verify
// checks it for every trace of a bounded length.
module folds {
  import grist.* from "../grist"

  // the earlier definitions
  pure def oldConditionHolds(log: List[Event], readPos: int, reads: Set[Read]): bool =
    if (readPos == log.length()) true
    else and {
      0 <= readPos,
      readPos <= log.length(),
      log.indices().forall(i => i < readPos or reads.forall(r => not(matches(log[i], r)))),
    }
  pure def oldExistsTagged(log: List[Event], typ: str, key: str, value: str): bool =
    log.indices().exists(i => matches(log[i], { typ: typ, tags: Set((key, value)) }))
  pure def oldNextOf(log: List[Event], from: int, typ: str): int =
    val after = log.indices().filter(i => i >= from and log[i].typ == typ)
    if (after.size() == 0) 0 else after.fold(log.length(), (m, i) => if (i < m) i else m) + 1
  pure def oldKeyStable(effects: List[Effect], processor: str, trigger: int, key: str): bool =
    effects.indices().forall(i => effects[i]._1 == processor implies ((effects[i]._2 == trigger) == (effects[i]._3 == key)))
  pure def oldHolds(log: List[Event], e: Event): bool = log.indices().exists(i => log[i] == e)
  pure def oldHoldsUnknown(log: List[Event], processor: str, trigger: int): bool =
    log.indices().exists(i => log[i].typ == "grist.unknown" and log[i].strs[0] == processor and log[i].ints == [trigger])
  pure def oldAtMostOnceOutcomesLogged(log: List[Event], effects: List[Effect], processor: str, success: int => List[Event]): bool =
    effects.indices().forall(i => effects[i]._1 == processor implies (
      if (effects[i]._4) success(effects[i]._2).foldl(true, (b, e) => b and oldHolds(log, e))
      else oldHolds(log, failed(processor, effects[i]._2)) or oldHoldsUnknown(log, processor, effects[i]._2)))
  pure def oldCallKeyStable(effects: List[Effect], calls: List[CallKey], processor: str, trigger: int, key: str): bool = and {
    oldKeyStable(effects, processor, trigger, key),
    calls.indices().forall(i => calls[i]._1 == processor implies ((calls[i]._2 == trigger) == (calls[i]._3 == key))),
  }
  pure def oldKeyCalled(calls: List[CallKey], key: str): bool = calls.indices().exists(i => calls[i]._3 == key)

  // the generated nextID (grist/internal/gen), before and after
  pure val IDS = ["i1", "i2", "i3"]
  pure def oldNextID(answered: Set[str]): Set[str] =
    IDS.foldl(Set(), (s, i) => if (s == Set() and not(answered.contains(i))) Set(i) else s)
  pure def nextID(answered: Set[str]): Set[str] =
    val first = IDS.foldl("", (s, i) => if (s == "" and not(answered.contains(i))) i else s)
    if (first == "") Set() else Set(first)

  // the shapes: two types and grist's own, two processors, positions 1-2
  pure val TYPES = Set("a", "b", "grist.unknown")
  pure val PROCS = Set("p", "q")
  pure val KEYS = Set("k1", "k2")
  pure def event(typ: str, s: str, n: int, tagged: bool): Event =
    { typ: typ, strs: [s], ints: [n], tags: if (tagged) Set(("k", s)) else Set(), cause: ("command", s) }
  pure val READS: Set[Set[Read]] = Set(Set(), Set({ typ: "a", tags: Set() }), Set({ typ: "a", tags: Set(("k", "p")) }, { typ: "b", tags: Set() }))
  pure def success(t: int): List[Event] = [event("a", "p", t, true)]

  var log: List[Event]
  var effects: List[Effect]
  var calls: List[CallKey]
  var answered: Set[str]

  action init = all { log' = [], effects' = [], calls' = [], answered' = Set() }
  action step = any {
    {
      nondet typ = TYPES.oneOf()
      nondet s = PROCS.oneOf()
      nondet n = 1.to(2).oneOf()
      nondet tagged = Bool.oneOf()
      all { log' = log.append(event(typ, s, n, tagged)), effects' = effects, calls' = calls, answered' = answered }
    },
    {
      nondet p = PROCS.oneOf()
      nondet t = 1.to(2).oneOf()
      nondet k = KEYS.oneOf()
      nondet ok = Bool.oneOf()
      any {
        all { effects' = effects.append((p, t, k, ok)), log' = log, calls' = calls, answered' = answered },
        all { calls' = calls.append((p, t, k)), log' = log, effects' = effects, answered' = answered },
      }
    },
    {
      nondet id = Set("i1", "i2", "i3").oneOf()
      all { answered' = answered.union(Set(id)), log' = log, effects' = effects, calls' = calls }
    },
  }

  val agree = and {
    0.to(log.length() + 1).forall(pos => READS.forall(rs => conditionHolds(log, pos, rs) == oldConditionHolds(log, pos, rs))),
    TYPES.forall(typ => PROCS.forall(v => existsTagged(log, typ, "k", v) == oldExistsTagged(log, typ, "k", v))),
    0.to(log.length() + 1).forall(from => TYPES.forall(typ => nextOf(log, from, typ) == oldNextOf(log, from, typ))),
    PROCS.forall(p => 1.to(2).forall(t => KEYS.forall(k => and {
      keyStable(effects, p, t, k) == oldKeyStable(effects, p, t, k),
      callKeyStable(effects, calls, p, t, k) == oldCallKeyStable(effects, calls, p, t, k),
      holdsUnknown(log, p, t) == oldHoldsUnknown(log, p, t),
      keyCalled(calls, k) == oldKeyCalled(calls, k),
      holds(log, event("a", p, t, true)) == oldHolds(log, event("a", p, t, true)),
    }))),
    PROCS.forall(p => atMostOnceOutcomesLogged(log, effects, p, success) == oldAtMostOnceOutcomesLogged(log, effects, p, success)),
    nextID(answered) == oldNextID(answered),
  }
}
