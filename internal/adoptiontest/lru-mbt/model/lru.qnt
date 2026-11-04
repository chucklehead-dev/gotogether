// Independent abstract contract for a sequential least-recently-used cache.
// Authored from the agreed public contract, without implementation inspection.
module lru {
  type Callback = { key: str, value: int }
  type Observation = {
    keys: List[str], values: str -> int, count: int, callbacks: Callback -> int,
    found: bool, value: int, changed: bool, evicted: bool, removed: int,
  }
  type Call = Init | Add({ key: str, value: int }) | Get({ key: str })
    | Peek({ key: str }) | Remove({ key: str }) | Resize({ size: int })

  pure val KEYS = Set("a", "b", "c")
  pure val VALUES = Set(0, 1, 2)
  var data: str -> int
  var order: List[str] // oldest to newest, with each present key once
  var capacity: int
  var observed: Observation
  var last: Call

  pure def keySet(xs: List[str]): Set[str] = xs.indices().map(i => xs[i])

  pure def without(xs: List[str], key: str): List[str] =
    xs.foldl(List(), (out, x) => if (x == key) out else out.append(x))

  pure def callbacksFor(entries: str -> int, removed: List[str]): Callback -> int =
    removed.foldl(Map(), (out, k) => out.put({ key: k, value: entries.get(k) }, 1))

  pure def view(entries: str -> int, orderedKeys: List[str], callbacks: Callback -> int,
      found: bool, value: int, changed: bool, evicted: bool, removed: int): Observation = {
    keys: orderedKeys, values: entries, count: orderedKeys.length(), callbacks: callbacks,
    found: found, value: value, changed: changed, evicted: evicted, removed: removed,
  }

  action install(entries: str -> int, orderedKeys: List[str], limit: int,
      callbacks: Callback -> int, found: bool, value: int, changed: bool,
      evicted: bool, removed: int, call: Call): bool = all {
    data' = entries, order' = orderedKeys, capacity' = limit,
    observed' = view(entries, orderedKeys, callbacks, found, value, changed, evicted, removed),
    last' = call,
  }

  action init = install(Map(), List(), 2, Map(), false, 0, false, false, 0, Init)

  action add(key: str, value: int): bool = {
    val promoted = without(order, key).append(key)
    val evicted = promoted.length() > capacity
    val removed = if (evicted) promoted.slice(0, 1) else List()
    val kept = if (evicted) promoted.slice(1, promoted.length()) else promoted
    val updated = data.put(key, value)
    all {
      KEYS.contains(key), VALUES.contains(value),
      install(keySet(kept).mapBy(k => updated.get(k)), kept, capacity,
        callbacksFor(updated, removed), false, 0, false, evicted, 0, Add({ key: key, value: value })),
    }
  }

  action lookup(key: str): bool = {
    val found = data.keys().contains(key)
    val promoted = if (found) without(order, key).append(key) else order
    all {
      KEYS.contains(key),
      install(data, promoted, capacity, Map(), found, if (found) data.get(key) else 0,
        false, false, 0, Get({ key: key })),
    }
  }

  action peek(key: str): bool = {
    val found = data.keys().contains(key)
    all {
      KEYS.contains(key),
      install(data, order, capacity, Map(), found, if (found) data.get(key) else 0,
        false, false, 0, Peek({ key: key })),
    }
  }

  action remove(key: str): bool = {
    val found = data.keys().contains(key)
    val kept = without(order, key)
    val removed = if (found) List(key) else List()
    all {
      KEYS.contains(key),
      install(keySet(kept).mapBy(k => data.get(k)), kept, capacity,
        callbacksFor(data, removed), false, 0, found, false, 0, Remove({ key: key })),
    }
  }

  action resize(limit: int): bool = {
    val count = if (order.length() > limit) order.length() - limit else 0
    val removed = order.slice(0, count)
    val kept = order.slice(count, order.length())
    all {
      limit >= 1, limit <= 3,
      install(keySet(kept).mapBy(k => data.get(k)), kept, limit,
        callbacksFor(data, removed), false, 0, false, false, count, Resize({ size: limit })),
    }
  }

  action step = {
    nondet key = KEYS.oneOf()
    nondet value = VALUES.oneOf()
    nondet limit = 1.to(3).oneOf()
    any { add(key, value), lookup(key), peek(key), remove(key), resize(limit) }
  }

  val bounded = capacity >= 1 and capacity <= 3 and data.keys().size() <= capacity
  val uniqueKeys = keySet(order).size() == order.length() and keySet(order) == data.keys()
  val snapshotAgrees = observed.keys == order and observed.values == data and observed.count == order.length()

  val coherent = bounded and uniqueKeys and snapshotAgrees

  // Forced witnesses discriminate recency changes without relying on random discovery.
  run mbtPeekDoesNotPromoteTest = init.then(add("a", 1)).then(add("b", 2)).then(peek("a"))
    .expect(order == List("a", "b"))
    .then(add("c", 0)).expect(order == List("b", "c") and observed.callbacks == Map(({ key: "a", value: 1 }, 1)))

  run mbtGetPromotesTest = init.then(add("a", 1)).then(add("b", 2)).then(lookup("a"))
    .expect(order == List("b", "a") and observed.found and observed.value == 1)
    .then(add("c", 0)).expect(order == List("a", "c") and observed.callbacks == Map(({ key: "b", value: 2 }, 1)))

  run mbtResizeEvictsOldestTest = init.then(resize(3)).then(add("a", 0)).then(add("b", 1)).then(add("c", 2))
    .then(resize(1)).expect(order == List("c") and observed.removed == 2 and
      observed.callbacks == Map(({ key: "a", value: 0 }, 1), ({ key: "b", value: 1 }, 1)))

  run mbtUpdateAndRemoveTest = init.then(add("a", 0)).then(add("b", 1)).then(add("a", 2))
    .expect(order == List("b", "a") and not(observed.evicted))
    .then(remove("b")).expect(observed.changed and observed.callbacks == Map(({ key: "b", value: 1 }, 1)))
    .then(remove("b")).expect(not(observed.changed) and observed.callbacks == Map())

  run mbtMissesAreNonmutatingTest = init.then(add("a", 1)).then(lookup("b"))
    .expect(not(observed.found) and order == List("a"))
    .then(peek("c")).expect(not(observed.found) and order == List("a"))
}
