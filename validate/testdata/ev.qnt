// A counter whose states recur (resume_test.go): inc from a resumed start,
// tick with unrecorded bumps (hidden); at most one, eventually two, which
// no one path has.
module ev {
  var x: int
  action init = x' = 0
  action inc = x' = x + 1
  action tick = x' = x
  action bump = x' = x + 1
  action resumeAny = { nondet v = 0.to(3).oneOf() x' = v }
  action step = any { inc, tick, bump }
  val atMostOne: bool = x <= 1
  val small: bool = x <= 10
  val two: bool = x == 2
}
