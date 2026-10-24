// A linearizable register per key, as a replicated store's clients must
// see it: a write sets the key's value, a read returns it. Replicas appear
// only in what a probe observed after the system settled (observe): the
// store converged when every replica observed holds every key's value.
// Keys are independent (no action reads one key and writes another), so a
// history of them is checked linearized key by key (qtest.LinearizeByKey).
module quorumkv {
  const KEYS: Set[str]
  const REPLICAS: Set[str]

  var reg: str -> int
  // what the probe saw at each replica, by (replica, key)
  var seen: (str, str) -> int

  action init = all { reg' = KEYS.mapBy(_ => 0), seen' = Map() }

  action write(key: str, value: int): bool = all {
    KEYS.contains(key),
    reg' = reg.set(key, value),
    seen' = seen,
  }

  // a read changes nothing; the binding checks the value it returned
  action read(key: str): bool = all {
    KEYS.contains(key),
    reg' = reg,
    seen' = seen,
  }

  action observe(replica: str, key: str, value: int): bool = all {
    REPLICAS.contains(replica), KEYS.contains(key),
    reg' = reg,
    seen' = seen.put((replica, key), value),
  }

  action step = nondet k = KEYS.oneOf() nondet r = REPLICAS.oneOf() nondet v = 0.to(3).oneOf()
    any { write(k, v), read(k), observe(r, k, v) }

  // every replica the probe observed holds every key's latest value: an
  // eventual property, judged when the run settled
  val converged: bool = seen.keys().forall(rk => seen.get(rk) == reg.get(rk._2))
}
