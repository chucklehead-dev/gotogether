// A model whose simulation runs as long as it is asked to: the
// evaluator a cancelled run must not leave behind.
module spin {
  var n: int

  action init = n' = 0

  action step = any {
    n' = n + 1,
    n' = n - 1,
  }
}
