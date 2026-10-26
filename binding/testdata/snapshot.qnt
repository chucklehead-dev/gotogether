// Snapshot actions (snapshot_test.go): restore and setX assign every
// variable (setX's y through sameY), onlyX leaves y unassigned.
module snap {
  var x: int
  var y: int
  action sameY = y' = y
  action init = all { x' = 0, y' = 0 }
  action restore(a: int, b: int): bool = all { x' = a, y' = b }
  action setX(v: int): bool = all { x' = v, sameY }
  action onlyX(v: int): bool = x' = v
  action step = nondet v = 1.to(3).oneOf() setX(v)
}
