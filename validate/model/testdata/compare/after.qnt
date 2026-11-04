// Finite handshake: send, optional one retry, acknowledgment; no hidden steps.
module after {
  var phase: int
  var retries: int

  action init = all { phase' = 0, retries' = 0 }
  action send = all { phase == 0, phase' = 1, retries' = retries }
  action retry = all { phase == 1, retries == 0, phase' = phase, retries' = 1 }
  action ack = all { phase <= 1, phase' = 2, retries' = retries }
  action step = any { send, retry, ack }
}
