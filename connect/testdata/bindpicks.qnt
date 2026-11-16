// The shapes connect.Bind's check attributes a step's nondets to its
// actions through (golem's attempt driver, #144): quint run --mbt records
// every nondet at every step, so outcome is in attempt's steps too, but
// it is failed's choice. d feeds attempt and retry both; backoff is bound
// in retry's own body; probe is applied under a match, which the check
// does not follow. The check runs on bindpicksDesign, an instance of the
// model with its constant, the Main a connect run is given.
module bindpicks {
  type Kind = Fast | Slow
  const OUTCOMES: Set[str]
  var tries: int
  var log: List[str]

  action init = all { tries' = 0, log' = [] }

  action attempt(d: int): bool = all { tries' = tries + d, log' = log }

  action failed(outcome: str): bool = all { tries' = tries, log' = log.append(outcome) }

  action retry(d: int): bool = {
    nondet backoff = 1.to(3).oneOf()
    all { tries' = tries + d + backoff, log' = log }
  }

  action probe(d: int): bool = all { tries' = tries, log' = log }

  action step = {
    nondet d = 1.to(3).oneOf()
    nondet outcome = OUTCOMES.oneOf()
    nondet kind = Set(Fast, Slow).oneOf()
    any {
      attempt(d),
      failed(outcome),
      retry(d),
      match kind { | Fast => probe(d) | Slow => probe(d) },
    }
  }

  // what attempt's result is compared with (Returns)
  def triesAfter(d: int): int = tries + d
}

module bindpicksDesign {
  import bindpicks(OUTCOMES = Set("lost", "late")).*
}
