// The liveness search's terminal case (simulate -graph, cmd/gotogether's
// TestSimulateGraphEventually): a job is counted to two and finished, or
// given up at one, which ends the model too (simulate.terminal) with
// finished false: a run that ends there ends with it false, which the
// search reports as a lasso that stays there forever.
module hot_ends {
  var n: int
  var done: bool

  action init = all { n' = 0, done' = false }
  action inc = all { n < 2, n' = n + 1, done' = done }
  action finish = all { n == 2, not(done), n' = n, done' = true }
  action giveUp = all { n == 1, not(done), n' = 5, done' = done }
  action step = any { inc, finish, giveUp }

  val finished = done
}
