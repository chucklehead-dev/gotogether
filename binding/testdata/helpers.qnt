// A frame condition other actions call (sameY) is a helper, not a step:
// step names inc and write only (model_test.go).
module helpers {
  var x: int
  var y: int
  action sameY = y' = y
  action init = all { x' = 0, y' = 0 }
  action inc = all { x' = x + 1, sameY }
  action write(v: int): bool = all { x' = v, sameY }
  action step = any { inc, nondet v = 1.to(3).oneOf() write(v) }
}
