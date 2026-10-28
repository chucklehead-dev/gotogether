// A step that does more than call its branch (graph_test.go): it counts
// its steps itself, so inc alone assigns too little to replay a
// transition, and the counterexample's replay takes step. reset is the
// binding's but no branch of step's.
module graph_wrap {
  var x: int
  var n: int

  action init = all { x' = 0, n' = 0 }
  action inc = x' = x + 1
  action dec = all { x > 5, x' = x - 1 }
  action reset = all { x' = 0, n' = 0 }
  action step = all { n < 3, any { inc, dec }, n' = n + 1 }

  val small = x < 3
}
