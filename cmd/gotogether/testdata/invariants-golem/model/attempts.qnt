// Attempts, one of two models whose annotations share a tree
// (invariants_test.go, TestInvariantsTwoModels).
module attempts {
  var n: int

  /// No attempt is lost, whatever
  /// restores it. Said once more.
  val noLostAttempt = n >= 0

  /// At most five attempts.
  val boundedAttempts = n <= 5

  /// The model's invariant, all it must hold.
  val safe = and { noLostAttempt, boundedAttempts }

  // a scenario's target: nothing enforces it, by design
  val reachedFive = n == 5

  action init = n' = 0
  action attempt = all { n < 5, n' = n + 1 }
  action step = attempt
}
