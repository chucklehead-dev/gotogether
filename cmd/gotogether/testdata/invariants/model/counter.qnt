// A counter with a cap: the fixture of gotogether invariants
// (invariants_test.go). counter is its instance.
module counterBase {
  const CAP: int
  var n: int

  /// n never exceeds the cap.
  val withinCap = n <= CAP

  // n is never negative.
  val nonNegative = n >= 0

  val atCap = n == CAP

  // an int, no property
  val twice = n * 2

  action init = n' = 0
  action inc = all { n < CAP, n' = n + 1 }
  action reset = n' = 0
  action step = any { inc, reset }
}

module counter {
  import counterBase(CAP = 3).*
}
