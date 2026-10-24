// account.qnt with an eventual property: money left in the account when
// a run ends (funds.binding.yaml).
module funds {
  const LIMIT: int
  var balance: int
  var ops: int

  action init = all { balance' = 0, ops' = 0 }
  action deposit(amount: int): bool = all { amount > 0, balance' = balance + amount, ops' = ops + 1 }
  action withdraw(amount: int): bool = all { amount > 0, amount <= balance, balance' = balance - amount, ops' = ops + 1 }
  action step = { nondet a = 1.to(LIMIT).oneOf() any { deposit(a), withdraw(a) } }

  val nonNegative: bool = balance >= 0
  val underLimit: bool = balance <= LIMIT
  // money left in the account: an eventual property (funds.binding.yaml)
  val funded: bool = balance > 0
}
