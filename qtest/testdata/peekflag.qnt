// A counter, a flag and a peek at the counter: the invariant needs two
// increments by the time the flag is set (TestResolvePendingInvariantsAgainstBruteForce).
module peekflag {
  var x: int
  var done: bool
  action init = all { x' = 0, done' = false }
  action inc = all { x' = x + 1, done' = done }
  action finish = all { x' = x, done' = true }
  action peek = all { x' = x, done' = done }
  action step = any { inc, finish, peek }
  val ok = done implies x >= 2
}
