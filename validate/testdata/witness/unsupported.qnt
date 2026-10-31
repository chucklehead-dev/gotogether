module unsupportedWitness {
  type Tag = A(int) | B
  var phase: int
  var tag: Tag
  action init = all { phase' = 0, tag' = B }
  action pick = {
    nondet value = Set(0, 1).oneOf()
    all { phase == 0, phase' = 1, tag' = A(value) }
  }
  action step = pick
  val safe = phase >= 0
}
