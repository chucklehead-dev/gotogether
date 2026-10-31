module mergedWitness {
  var phase: int
  var chosen: int
  action init = all { phase' = 0, chosen' = 0 }
  action pick = {
    nondet value = Set(-1, 1).oneOf()
    all { phase == 0, phase' = 1, chosen' = value }
  }
  action forget = all { phase == 1, phase' = 2, chosen' = 0 }
  action onlyNegative = all { phase == 1, chosen < 0, phase' = phase, chosen' = chosen }
  action step = any { pick, forget, onlyNegative }
  val safe = chosen >= 0
  val completed = phase == 2
}
