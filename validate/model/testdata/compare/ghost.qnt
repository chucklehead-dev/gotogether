// Finite handshake: send, optional one retry, acknowledgment; no hidden steps.
module ghost {
  var phase: int
  var retries: int
  var ghost: bool
  action init = all { phase' = 0, retries' = 0, ghost' = false }
  action send = all { phase == 0, phase' = 1, retries' = retries, ghost' = not(ghost) }
  action retry = all { phase == 1, retries == 0, phase' = phase, retries' = 1, ghost' = not(ghost) }
  action ack = all { phase == 1, phase' = 2, retries' = retries, ghost' = not(ghost) }
  action step = any { send, retry, ack }
}
