// A model of 4,096 states, each a pair below 64 (internal/qeval's
// memory_test.go, cmd/gotogether's graph_memory_test.go): enough
// admissions for the explorer to read the process's memory many times.
module wide {
  var x: int
  var y: int
  action init = all { x' = 0, y' = 0 }
  action incX = all { x < 63, x' = x + 1, y' = y }
  action incY = all { y < 63, x' = x, y' = y + 1 }
  action step = any { incX, incY }
}
