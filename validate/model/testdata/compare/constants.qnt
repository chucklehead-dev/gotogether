module constants {
  const LIMIT: int
  var x: int
  action init = x' = 0
  action advance = all { x < LIMIT, x' = x + 1 }
  action step = advance
}
