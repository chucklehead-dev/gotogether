// A gate that lets through only when open: a model for vacuity tests,
// whose invariant says something only once someone has passed.
module gate {
  var opened: bool
  var passes: int

  action init = all { opened' = false, passes' = 0 }
  action open = all { opened' = true, passes' = passes }
  action pass = all { opened, opened' = opened, passes' = passes + 1 }
  action step = any { open, pass }

  val passedWhenOpen = passes > 0 implies opened
  val neverNegative = passes >= 0 // no antecedent
}
