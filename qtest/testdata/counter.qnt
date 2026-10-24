// A counter that must stay at most one (qtest/linearize_test.go): two
// increments and a decrement, concurrent, violate it in some orders only.
module counter {
  var x: int
  action init = x' = 0
  action inc: bool = x' = x + 1
  action dec: bool = x' = x - 1
  action step = any { inc, dec }
  val atMostOne: bool = x <= 1
}
