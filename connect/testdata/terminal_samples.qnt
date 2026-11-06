// Every sample ends before the step limit; seed 42 includes initial-terminal
// and longer runs. Asking for all five must not duplicate long runs and drop
// the initial-terminal samples.
module terminal_samples {
  var n: int
  var bound: int
  action init = {
    nondet b = 0.to(4).oneOf()
    all { n' = 0, bound' = b }
  }
  action step = all { n < bound, n' = n + 1, bound' = bound }
}
