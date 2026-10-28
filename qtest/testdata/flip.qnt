module flip {
  var x: int
  action init = x' = 0
  action flipit = any { x' = 1, x' = 2, x' = 3, x' = 4 }
  action step = flipit
}
