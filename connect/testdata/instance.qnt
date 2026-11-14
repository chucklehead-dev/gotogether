// A model bound through an instance module, the usual shape of a design's
// binding: nothing init or step uses reads atMax, half or twice, so quint's
// flattening drops them unless asked to keep them (require_test.go).
module counter {
  const MAX: int
  var n: int
  action init = n' = 0
  action inc = all { n < MAX, n' = n + 1 }
  action step = inc
  val atMax = n == MAX
  val half = n * 2 == MAX
  pure def twice(x: int): int = 2 * x
}

module design {
  import counter(MAX = 4).*
}
