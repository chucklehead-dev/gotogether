// bank's inputs without its guards, for a Differential with IgnoreModel:
// a withdrawal may exceed the balance, which bank.qnt never lets happen.
// Its states describe the inputs, not the design.
module bank_inputs {
  pure val ACCOUNTS = Set("alice", "bob")
  pure val MAX = 5

  var balances: str -> int
  var ops: int

  action init = all { balances' = ACCOUNTS.mapBy(_ => 0), ops' = 0 }

  action deposit(account: str, amount: int): bool = all {
    balances' = balances.setBy(account, b => b + amount),
    ops' = ops + 1,
  }

  action withdraw(account: str, amount: int): bool = all {
    balances' = balances,
    ops' = ops,
  }

  action step = {
    nondet account = ACCOUNTS.oneOf()
    nondet amount = 1.to(MAX).oneOf()
    any { deposit(account, amount), withdraw(account, amount) }
  }
}
