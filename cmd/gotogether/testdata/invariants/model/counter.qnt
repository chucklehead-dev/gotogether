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

  // What the model leaves out (ROADMAP CW3): each a reviewed line, its
  // own name, one stated by a val (no property, then), two in prose.
  // @assume noOverflow = belowMaxInt: n + 1 never overflows; Go's int wraps past math.MaxInt
  // @assume oneCaller: one caller at a time; the real Counter has no lock
  // @assume capFixed: the cap never changes; the real Cap is a field anyone may set
  val belowMaxInt = n < 9223372036854775807

  action init = n' = 0
  action inc = all { n < CAP, n' = n + 1 }
  action reset = n' = 0
  // nothing implements idle: the actions table shows it
  action idle = n' = n
  action step = any { inc, reset, idle }
}

module counter {
  import counterBase(CAP = 3).*
}
