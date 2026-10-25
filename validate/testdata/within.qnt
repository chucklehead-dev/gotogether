// A node that crashes and heals, and elects a leader once up: within a
// bound after it heals it must have one (bounded response, within_test.go).
module within {
  var up: bool
  var leader: bool

  action init = all { up' = true, leader' = true }
  action crash = all { up' = false, leader' = false }
  action heal = all { not(up), up' = true, leader' = leader }
  action elect = all { up, up' = up, leader' = true }
  action tick = all { up' = up, leader' = leader }
  action step = any { crash, heal, elect, tick }

  val healed = up
  val hasLeader = leader
}
