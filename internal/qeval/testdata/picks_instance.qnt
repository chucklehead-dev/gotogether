// ActionPicks under an instance module, the Main the skills recommend for
// connect: quint flattens the import (gateway::attempts::attempt) and
// aliases step (q::step = gateway::attempts::step), and the attribution
// must come out as it does on attempts itself (golem's shape, #144).
module attempts {
  const CALLS: Set[str]
  var on: str -> str
  action init = on' = CALLS.mapBy(c => "none")
  action start(c: str): bool = on' = on.set(c, "s")
  action attempt(c: str, d: str): bool = on' = on.set(c, d)
  action failed(c: str, outcome: str): bool = { val d = on.get(c) all { d != "", on' = on.set(c, outcome) } }
  action step = {
    nondet c = CALLS.oneOf()
    nondet d = Set("d1", "d2").oneOf()
    nondet outcome = Set("x", "y").oneOf()
    any { start(c), attempt(c, d), failed(c, outcome) }
  }
}

module gateway { import attempts(CALLS = Set("a", "b")).* }
