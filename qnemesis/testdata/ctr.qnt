module ctr {
  var count: int
  var up: bool
  action init = all { count' = 0, up' = true }
  action inc = all { up, count' = count + 1, up' = up }
  action crash = all { up, count' = count, up' = false }
  action restart = all { not(up), count' = count, up' = true }
  // a client saw the server's count n: the server counted to n
  action saw(n: int): bool = all { n <= count, count' = count, up' = up }
  action step = any { inc, crash, restart, { nondet n = 0.to(5).oneOf() saw(n) } }
}
