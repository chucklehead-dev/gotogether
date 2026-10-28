// Integers of 16 digits and more, which quint 0.33's Rust backend writes
// into ITF as bignumber.js objects ({s, e, c}), beside a record of those
// very fields, which it writes as a record (ROADMAP I11's note).
module bigint {
  type Digits = { s: int, e: int, c: List[int] }
  var balance: int
  var shape: Digits

  action init = all { balance' = 0, shape' = { s: 1, e: 15, c: [10] } }
  action deposit(amount: int): bool = all { amount > 0, balance' = balance + amount, shape' = shape }
  action withdraw(amount: int): bool = all { amount > 0, amount <= balance, balance' = balance - amount, shape' = shape }
  action step = any { deposit(1000000000000000), withdraw(1) }

  val nonNegative: bool = balance >= 0
}
