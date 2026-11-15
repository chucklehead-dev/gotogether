// Two designs of one lease (matrix_test.go, ROADMAP CW2): an old leader,
// paused, resumes after a takeover and writes. Under "lease" nothing stops
// it; under "fence" the store refuses a write whose epoch is behind its
// own. The design matrix should show fencing holding what leasing breaks.
module design {
  const DESIGN: str

  var epoch: int        // the store's epoch: the newest leader's
  var oldWrote: bool    // the old leader (epoch 1) has written
  var staleWrites: int  // writes accepted from behind the store's epoch

  action init = all { epoch' = 1, oldWrote' = false, staleWrites' = 0 }

  // a new leader takes over, once
  action takeover = all { epoch == 1, epoch' = 2, oldWrote' = oldWrote, staleWrites' = staleWrites }

  // the old leader writes with its epoch, 1
  action writeOld = all {
    DESIGN == "lease" or epoch <= 1,
    epoch' = epoch,
    oldWrote' = true,
    staleWrites' = if (epoch > 1) staleWrites + 1 else staleWrites,
  }

  action step = any { takeover, writeOld }

  // no write lands from a leader the store has moved past
  val noStaleWrite = staleWrites == 0
  // the old leader never writes at all: every design lets it, before the takeover
  val oldNeverWrites = not(oldWrote)
}
