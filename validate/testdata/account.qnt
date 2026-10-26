// A second, unrelated model: gotogether's core must work for it with only a
// binding. An account that must never be overdrawn.
module account {
  const LIMIT: int
  var balance: int
  var ops: int

  action init = all { balance' = 0, ops' = 0 }
  action deposit(amount: int): bool = all { amount > 0, balance' = balance + amount, ops' = ops + 1 }
  action withdraw(amount: int): bool = all { amount > 0, amount <= balance, balance' = balance - amount, ops' = ops + 1 }
  // the whole state, recorded at the head of a log (snapshot.yaml)
  action restore(b: int, n: int): bool = all { b >= 0, balance' = b, ops' = n }
  // the states a log that lost its head may start in (resume.yaml): any
  // balance from -2 to LIMIT, the negative ones ruled out by nonNegative
  action resumeAny = { nondet b = (-2).to(LIMIT).oneOf() all { balance' = b, ops' = 0 } }
  action step = { nondet a = 1.to(LIMIT).oneOf() any { deposit(a), withdraw(a) } }

  val nonNegative: bool = balance >= 0
  val underLimit: bool = balance <= LIMIT
}
