// A model the Go evaluator follows until its integer passes 64 bits
// (graph_test.go): the explorer must stop there, not call it complete.
module grow {
  var x: int
  action init = x' = 2
  action step = x' = x * x
  val positive = x > 0
}
