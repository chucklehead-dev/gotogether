// within.qnt's node, its response a val over an infinite set, which the
// Go evaluator refuses (within_test.go)
module within_refused {
  var up: bool
  var leader: bool

  action init = all { up' = true, leader' = true }
  action crash = all { up' = false, leader' = false }
  action heal = all { not(up), up' = true, leader' = leader }
  action elect = all { up, up' = up, leader' = true }
  action step = any { crash, heal, elect }

  val healed = up
  val led = leader and Int.exists(i => i == 1)
}
