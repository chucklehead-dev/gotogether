// The random simulation's tests (sample_test.go): a small model whose
// every state is reachable (x in 0..5, y in 0..3: 24 states), with the
// choices a sampler must not get wrong: a nondet most of whose values
// disable the action (bump: one k of six), an any {} with a branch never
// enabled (swap's first), a helper action called by another (keep), a
// wrapper (again), an anonymous branch, and a choice in init.
module sample {
  var x: int
  var y: int

  action keep = all { x' = x, y' = y }

  action init = {
    nondet s = 0.to(2).oneOf()
    all { x' = s, y' = 0 }
  }

  action bump(k: int): bool = all { k == x + 1, x < 5, x' = k, y' = y }

  action swap = all {
    y < 3,
    any {
      all { x > 10, keep },
      all { x' = x, y' = y + 1 },
    },
  }

  action reset = all { x == 5, y == 3, x' = 0, y' = 0 }

  action again = reset

  // a value of 1000 of which one enables the action: a sampler that does
  // not backtrack disables it almost always
  action rare = { nondet r = 1.to(1000).oneOf() all { r == 7, x == 0, y == 0, x' = 1, y' = 1 } }

  action step = any {
    { nondet k = 1.to(6).oneOf() bump(k) },
    swap,
    again,
    rare,
    all { x == 2, y == 2, x' = 3, y' = 3 },
  }

  val small = x <= 5
  val boundedY = y <= 2
}
