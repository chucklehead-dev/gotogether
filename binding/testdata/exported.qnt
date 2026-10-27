// A model whose module re-exports a generated one (grist model's
// layout): its actions and variables are the exported module's, its vals
// its own and the exported one's.
module exportedLib {
  var n: int
  action init = n' = 0
  action add(by: int): bool = n' = n + by
  action step = { nondet by = 1.to(2).oneOf() add(by) }
  val small = n < 100
}

module exported {
  import exportedLib.*
  export exportedLib.*
  val nonNegative = n >= 0
}
