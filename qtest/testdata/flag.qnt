module flag {
  var x: int
  var done: bool
  action init = all { x' = 0, done' = false }
  action inc = all { x' = x + 1, done' = done }
  action finish = all { x' = x, done' = true }
  action step = any { inc, finish }
  val ok = done implies x >= 1
}
