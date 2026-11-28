// Every idiom quint's Veil backend refuses, one per val or action, each
// beside a form it proves (veilfrag_test.go names the lines).
module blocking {
  const JOINT: Set[str]
  type Status = Idle | Busy(int)
  var bal: str -> int
  var status: str -> Status
  var applied: Set[int]

  action init = all { bal' = JOINT.mapBy(_ => 0), status' = JOINT.mapBy(_ => Idle), applied' = Set() }

  action pay(src: str, dest: str): bool = all {
    src != dest,
    bal' = Map(src -> -1, dest -> 1),
    status' = status,
    applied' = applied,
  }
  action payOk(src: str, dest: str): bool = all {
    src != dest,
    bal' = Map(src -> -1).put(dest, 1),
    status' = status,
    applied' = applied,
  }
  action drop(a: str): bool = all {
    bal' = bal.keys().exclude(Set(a)).mapBy(k => bal.get(k)),
    status' = JOINT.mapBy(k => match status.get(k) { Idle => Idle | Busy(n) => Busy(n + 1) }),
    applied' = applied,
  }
  action dropOk(a: str): bool = all {
    bal' = JOINT.mapBy(k => if (k == a) 0 else bal.get(k)),
    status' = JOINT.mapBy(k => if (status.get(k) == Idle) Idle else status.get(k)),
    applied' = applied,
  }
  action step = { nondet a = JOINT.oneOf() nondet b = JOINT.oneOf() any { pay(a, b), payOk(a, b), drop(a), dropOk(a) } }

  pure def total(xs: Set[int]): int = xs.fold(0, (s, x) => s + x)
  val joint = JOINT.fold(0, (s, a) => s + bal.get(a))
  val appliedFold = applied.fold(0, (s, x) => s + x)
  val empty = applied.size() == 0
  val atMostOne = 1 >= applied.size()
  val counted = applied.size() == 10
  val literalKeys = Map("a" -> 1, "b" -> 2) != bal
  val others = JOINT.union(Set("y")).exclude(Set("x")).mapBy(k => 0) == bal
}
