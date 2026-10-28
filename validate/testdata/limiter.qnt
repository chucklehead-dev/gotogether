// A rate limiter: at most LIMIT clients admitted at once. A step the
// model refuses names why (H2): admit's second conjunct, 2 < 2, when two
// are admitted already.
module limiter {
  const LIMIT: int
  const CLIENTS: Set[str]
  var admitted: Set[str]

  action init = admitted' = Set()

  action admit(client: str): bool = all {
    not(admitted.contains(client)),
    admitted.size() < LIMIT,
    admitted' = admitted.union(Set(client)),
  }

  action release(client: str): bool = all {
    admitted.contains(client),
    admitted' = admitted.exclude(Set(client)),
  }

  action step = {
    nondet c = CLIENTS.oneOf()
    any { admit(c), release(c) }
  }

  val underLimit = admitted.size() <= LIMIT
}
