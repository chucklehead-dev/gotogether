// Settle-conditioned invariants: settled is never set by step, done is.
module settle {
  var x: int
  var settled: bool
  var done: bool

  action init = all { x' = 0, settled' = false, done' = false }
  action bump = all { x' = x + 1, settled' = settled, done' = done }
  action finish = all { x' = x, settled' = false, done' = true }
  // the settle the binding records; step never takes it
  action settle = all { x' = x, settled' = true, done' = done }
  action step = any { bump, finish }

  val settledCorrectly = settled implies x >= 0
  val doneCorrectly = done implies x >= 0
  val bothCorrectly = settled and done implies x >= 0
  val neverSettled = not(settled)
}
