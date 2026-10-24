// One register a probe observes after the system settles, which must
// eventually hold a value (qtest/resolve_test.go).
module probed {
  var x: int
  action init = x' = 0
  action write(value: int): bool = x' = value
  action observe: bool = x' = x
  action step = any { nondet v = 1.to(3).oneOf() write(v), observe }
  val written: bool = x > 0
}
