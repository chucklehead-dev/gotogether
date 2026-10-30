module payments {
  var phase: str
  var balance: int
  var debits: int
  var captures: int
  action init = all {phase' = "idle", balance' = 100, debits' = 0, captures' = 0}
  action begin = all {phase == "idle", phase' = "pending", balance' = balance, debits' = debits, captures' = captures}
  action finish = all {phase == "pending", phase' = "done", balance' = 90, debits' = 1, captures' = 1}
  action replay = all {phase == "done", phase' = phase, balance' = balance, debits' = debits, captures' = captures}
  action conflict = all {phase != "idle", phase' = phase, balance' = balance, debits' = debits, captures' = captures}
  action step = any {begin, finish, replay, conflict}
  val once = debits <= 1 and captures <= 1
  val conserved = balance + 10 * debits == 100
  val completed = phase == "done"
  run happyTest = init.then(begin).then(conflict).then(finish).then(replay).expect(once and conserved and completed)
}
