// Accesses a footprint records (footprint_test.go): each action or val
// reads and writes entries of the state's maps, or depends on all of it.
module footprint {
  var bal: str -> int
  var idx: str -> str
  var total: int

  action init = all { bal' = Map("a" -> 1, "b" -> 2), idx' = Map(), total' = 0 }

  def has(k: str): bool = bal.keys().contains(k)
  // entry by entry
  action deposit(k: str, n: int): bool = all {
    has(k), bal' = bal.setBy(k, v => v + n), idx' = idx, total' = total,
  }
  action open(k: str, key: str): bool = all {
    not(has(k)), bal' = bal.put(k, 0), idx' = idx.put(key, k), total' = 5,
  }
  action peek(k: str): bool = all { bal.get(k) > 0, bal' = bal, idx' = idx, total' = total }
  action move(a: str, b: str): bool = all {
    bal' = bal.set(a, bal.get(a) - 1).set(b, bal.get(b) + 1), idx' = idx, total' = total,
  }
  // whole
  action scan(n: int): bool = all { bal.keys().exists(k => bal.get(k) == n), bal' = bal, idx' = idx, total' = total }
  action count: bool = all { bal.keys().size() > 1, bal' = bal, idx' = idx, total' = total }
  action tally: bool = all { bal' = bal, idx' = idx, total' = total + 1 }
  action reset: bool = all { bal' = Map(), idx' = idx, total' = total }
  action same(k: str): bool = all { bal == bal.put(k, 1), bal' = bal, idx' = idx, total' = total }

  // judged vals
  val positive = bal.keys().forall(k => bal.get(k) >= 0)
  val both = and { positive, idx.keys().forall(x => has(idx.get(x))) }
  val notAll = not(bal.keys().forall(k => bal.get(k) > 1))
  val pairs = bal.keys().forall(a => bal.keys().forall(b => a == b or bal.get(a) != bal.get(b)))
  val usesPositive = positive or total > 0
  val cachedThenOr = and { positive, usesPositive }
  action step = any { deposit("a", 1), tally }
}
