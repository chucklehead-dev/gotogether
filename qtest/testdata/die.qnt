module die {
  var x: int
  action init = x' = 0
  action roll = { nondet v = 1.to(6).oneOf() x' = v }
  action step = roll
}
