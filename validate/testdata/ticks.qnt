module ticks {
  var c: int
  action init = c' = 0
  action tick: bool = all { c < 10, c' = c + 1 }
  action fire: bool = all { c >= 5, c' = c }
  action step = any { tick, fire }
}
