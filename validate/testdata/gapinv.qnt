// Two invariants that fail at different steps (absent_test.go's
// TestGapFailureWins): underLimit at the second deposit of 60, fewOps at
// the third operation.
module gapinv {
  var balance: int
  var ops: int
  action init = all { balance' = 0, ops' = 0 }
  action deposit(amount: int): bool = all { amount > 0, balance' = balance + amount, ops' = ops + 1 }
  action step = { nondet a = 1.to(100).oneOf() deposit(a) }
  val fewOps: bool = ops < 3
  val underLimit: bool = balance <= 100
}
