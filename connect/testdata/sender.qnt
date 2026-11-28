// -*- mode: Bluespec; -*-
// A gateway that attempts calls on deployments, for connect's effects
// tests (DESIGN §115): an attempt sends one request to the deployment it
// names, carrying that deployment's model (golem's failover bug kept the
// first deployment's); the gateway says hello once, when it starts; a
// commit sends nothing (it has no effects def, and a send there fails).
module sender {
  type Effect =
    | Send({ call: str, deployment: str, model: str })
    | Hello

  pure val CALLS = Set("c1", "c2")
  pure val DEPLOYMENTS = Set("d1", "d2")
  pure val MODELS = Map("d1" -> "m1", "d2" -> "m2")

  var attempted: str -> str // a call's last deployment
  var ended: Set[str]

  action init = all { attempted' = Map(), ended' = Set() }

  action attempt(c: str, d: str): bool = all {
    not(ended.contains(c)),
    attempted' = attempted.put(c, d),
    ended' = ended,
  }

  action commit(c: str): bool = all {
    attempted.keys().contains(c),
    not(ended.contains(c)),
    ended' = ended.union(Set(c)),
    attempted' = attempted,
  }

  action step = {
    nondet c = CALLS.oneOf()
    nondet d = DEPLOYMENTS.oneOf()
    any { attempt(c, d), commit(c) }
  }

  // what starting sends: a hello
  val initEffects: List[Effect] = [Hello]
  // a hello said twice, the same as one under match set
  val helloTwice: List[Effect] = [Hello, Hello]
  // what an attempt sends: the request, to d, with d's model
  def attemptEffects(c: str, d: str): List[Effect] = [Send({ call: c, deployment: d, model: MODELS.get(d) })]
  // the model a deployment serves (no list of effects: a def the
  // binding's check refuses as one)
  def modelOf(d: str): str = MODELS.get(d)
  // a def of a parameter commit does not have
  def commitTo(c: str, d: str): List[Effect] = []

  val noEnded = ended == Set()
}
