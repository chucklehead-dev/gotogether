// An invariant the Go evaluator cannot evaluate in one state (an integer
// past 64 bits at x = 1) and violated in another (x = 3), and a state
// only a stutter leaves (x = 4: read alone is enabled) (graph_test.go).
module graph_partial {
  var x: int

  action init = x' = 0
  action inc = all { x < 4, x' = x + 1 }
  action read = x' = x
  action step = any { inc, read }

  val small = if (x == 1) x * 4611686018427387904 * 4 > 0 else x < 3
}
