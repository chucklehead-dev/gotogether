// A sender whose messages the network may lose: send is the system's
// step, lose the environment's. A trace of it says which sends were lost
// before they got through, which FollowTraces turns into faults.
module flaky {
  var sent: int
  var lost: int

  action init = all { sent' = 0, lost' = 0 }
  action send = all { sent < 3, sent' = sent + 1, lost' = lost }
  action lose = all { sent < 3, lost' = lost + 1, sent' = sent }
  action step = any { send, lose }

  val atMostThree = sent <= 3
}
