// A key-value store as its clients must see it, linearizable: a write
// sets a key, a read returns it, a compare-and-set sets it if it holds the
// expected value, a transfer moves an amount from one key to another if
// the source holds it. lastOk is whether the last cas or transfer took
// effect, which the binding compares with what its client was told.
// Nodes appear only in what the probe observed after the system settled:
// the store converged when every node holds every key's value. A
// transfer reads one key and writes two, so a history is checked
// linearized whole (qtest.Linearize), not key by key.
module raftkv {
  const KEYS: Set[str]
  const NODES: Set[str]
  const INITIAL: int

  var kv: str -> int
  var lastOk: bool
  // what the probe saw at each node, by (node, key)
  var seen: (str, str) -> int

  action init = all { kv' = KEYS.mapBy(_ => INITIAL), lastOk' = true, seen' = Map() }

  action write(key: str, value: int): bool = all {
    KEYS.contains(key),
    kv' = kv.set(key, value), lastOk' = true, seen' = seen,
  }

  // a read changes nothing; the binding checks the value it returned
  action read(key: str): bool = all {
    KEYS.contains(key),
    kv' = kv, lastOk' = lastOk, seen' = seen,
  }

  action cas(key: str, expected: int, desired: int): bool = all {
    KEYS.contains(key),
    val ok = kv.get(key) == expected
    all {
      kv' = if (ok) kv.set(key, desired) else kv,
      lastOk' = ok, seen' = seen,
    },
  }

  action transfer(src: str, dest: str, amount: int): bool = all {
    KEYS.contains(src), KEYS.contains(dest),
    val ok = src != dest and kv.get(src) >= amount
    all {
      kv' = if (ok) kv.set(src, kv.get(src) - amount).set(dest, kv.get(dest) + amount) else kv,
      lastOk' = ok, seen' = seen,
    },
  }

  action observe(node: str, key: str, value: int): bool = all {
    NODES.contains(node), KEYS.contains(key),
    kv' = kv, lastOk' = lastOk,
    seen' = seen.put((node, key), value),
  }

  action step = nondet k = KEYS.oneOf() nondet j = KEYS.oneOf() nondet n = NODES.oneOf() nondet v = 0.to(12).oneOf()
    any { write(k, v), read(k), cas(k, INITIAL, v), transfer(k, j, 3), observe(n, k, v) }

  // every node the probe observed holds every key's value: an eventual
  // property, judged when the run settled
  val converged: bool = seen.keys().forall(nk => seen.get(nk) == kv.get(nk._2))

  // money is neither made nor lost by a transfer: with no write or cas, the
  // keys' sum stays what it was (a check of the model, simulated)
  val total: int = KEYS.fold(0, (s, k) => s + kv.get(k))
}
