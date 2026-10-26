// A user's journey through a workflow (submit, accept, ready, collect)
// whose end is an eventual val on a ghost flag: a prefix of it conforms,
// and only a run that reached collect finishes (qtest/finishes_test.go).
module journey {
  var stage: int
  // ghost: the journey reached its end; no action reads it
  var ended: bool
  action init = all { stage' = 0, ended' = false }
  action submit: bool = all { stage == 0, stage' = 1, ended' = ended }
  action accept: bool = all { stage == 1, stage' = 2, ended' = ended }
  action ready: bool = all { stage == 2, stage' = 3, ended' = ended }
  action collect: bool = all { stage == 3, stage' = 4, ended' = true }
  action step = any { submit, accept, ready, collect }
  val journeyEnded: bool = ended
}
