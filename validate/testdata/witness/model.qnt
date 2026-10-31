module witness {
  var phase: int
  var chosen: int

  action init = all { phase' = 0, chosen' = 0 }
  action pick = {
    nondet value = Set(0, 1).oneOf()
    all { phase == 0, phase' = 1, chosen' = value }
  }
  action anyPick = any {
    all { phase == 0, phase' = 1, chosen' = 0 },
    all { phase == 0, phase' = 1, chosen' = 1 }
  }
  action guardedPick = {
    nondet value = 0.to(2).oneOf()
    all { phase == 0, value == 1, phase' = 1, chosen' = value }
  }
  action riskyPick = {
    nondet value = Set(-1, 1).oneOf()
    all { phase == 0, phase' = 1, chosen' = value }
  }
  action donePick = {
    nondet value = Set(-1, 1).oneOf()
    all { phase == 0, phase' = 2, chosen' = value }
  }
  action finish(wanted: int): bool = all {
    phase == 1, chosen == wanted, phase' = 2, chosen' = chosen
  }
  action reset = all { phase == 2, phase' = 0, chosen' = 0 }
  action step = any {
    pick, anyPick, guardedPick, riskyPick, donePick,
    { nondet wanted = (-1).to(2).oneOf() finish(wanted) },
    reset
  }
  val safe = chosen >= 0
  val nonPositive = chosen <= 0
  val phaseBounded = 0.to(2).contains(phase)
  val completed = phase == 2
}
