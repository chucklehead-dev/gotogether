module tagged {
  var writes: Set[str]
  action init = writes' = Set()
  action write(p: str): bool = writes' = writes.union(Set(p))
  action look: bool = writes' = writes
  action restart: bool = writes' = writes
  action step = any { nondet p = Set("1", "2").oneOf() write(p), look, restart }
}
