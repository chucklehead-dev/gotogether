// The state-graph explorer's model (graph_test.go): two initial states,
// a deadlock, an action never enabled, a loop back to the start, and an
// invariant violated only in the deadlock.
module graph {
  var x: int
  var y: int

  action init = { nondet a = 0.to(1).oneOf() all { x' = a, y' = 0 } }

  action inc = all { x < 3, x' = x + 1, y' = y }
  action bump(by: int): bool = all { y + by <= 2, x' = x, y' = y + by }
  action reset = all { x == 2, y == 2, x' = 0, y' = 0 }
  action never = all { x > 5, x' = x, y' = y }

  action step = any {
    inc,
    { nondet by = 1.to(2).oneOf() bump(by) },
    reset,
    never,
  }

  val small = x + y < 5
  val positive = x >= 0
}
