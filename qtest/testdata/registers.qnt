// Independent registers, one per key: a history of them partitions by
// key (LinearizeByKey, linearize_test.go). A read observes its key's value.
module registers {
  pure val KEYS = Set("a", "b", "c")
  var regs: str -> int

  action init = regs' = KEYS.mapBy(_ => 0)

  action write(key: str, value: int): bool = all {
    KEYS.contains(key),
    regs' = regs.set(key, value),
  }

  action read(key: str): bool = all {
    KEYS.contains(key),
    regs' = regs,
  }

  action step = nondet k = KEYS.oneOf() nondet v = 1.to(3).oneOf() any { write(k, v), read(k) }
}
