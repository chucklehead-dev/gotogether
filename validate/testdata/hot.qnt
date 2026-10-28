// The liveness search's model (live_test.go, simulate -graph): a queue of
// at most two jobs, a client that submits them (an input), a worker that
// processes them, a read that changes nothing, and a retry that flips a
// flag while work is pending. With STORM, a retry blocks processing until
// the next one: a retry storm, which weak fairness does not rule out
// (process is not enabled throughout it).
module hot {
  const STORM: bool

  var pending: int
  var flag: bool

  action init = all { pending' = 0, flag' = false }

  action submit = all { pending < 2, pending' = pending + 1, flag' = flag }
  action process = all { pending > 0, not(STORM and flag), pending' = pending - 1, flag' = flag }
  action read = all { pending' = pending, flag' = flag }
  action retry = all { pending > 0, pending' = pending, flag' = not(flag) }

  action step = any { submit, process, read, retry }

  // every job processed: judged when a run settles
  val drained = pending == 0
}
