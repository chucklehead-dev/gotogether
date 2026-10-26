// A payments service's contract, as its clients, its database and its
// processor see it: an idempotency key is charged at most once and
// captured at most once, and every successful response for a key names
// the key's one charge. A client's request (request), what it heard back
// (response: an HTTP status, 0 for no response, and a charge's id), a
// charge the database committed (charge) and a capture the processor made
// (capture) are the steps.
//
// The steps commute: a charge may be recorded before or after the
// response that names it (the database's commit is recorded where it
// commits, a response where it arrives, and a real run's charges are read
// from the table after it). So the properties are about what was seen,
// whatever its order, and what must have happened is judged after the
// system settled.
module payd {
  // each key's request: its account and amount, as first asked
  var requested: str -> (str, int)
  // each key's charges: (id, account, amount); more than one is a double
  // charge
  var charges: str -> Set[(str, str, int)]
  // each key's captures at the processor (by the reference it was given)
  var captured: str -> int
  // the charge ids each key's successful (2xx) responses named
  var answered: str -> Set[str]

  action init = all {
    requested' = Map(),
    charges' = Map(),
    captured' = Map(),
    answered' = Map(),
  }

  pure def chargesOf(cs: str -> Set[(str, str, int)], k: str): Set[(str, str, int)] =
    if (cs.keys().contains(k)) cs.get(k) else Set()
  pure def idsOf(cs: str -> Set[(str, str, int)], k: str): Set[str] = chargesOf(cs, k).map(c => c._1)
  pure def answersOf(a: str -> Set[str], k: str): Set[str] = if (a.keys().contains(k)) a.get(k) else Set()
  pure def capturesOf(c: str -> int, k: str): int = if (c.keys().contains(k)) c.get(k) else 0

  action request(client: str, key: str, account: str, amount: int): bool = all {
    requested' = if (requested.keys().contains(key)) requested else requested.put(key, (account, amount)),
    charges' = charges, captured' = captured, answered' = answered,
  }

  // status 0 is no response (a timeout, a reset, a refusal): nothing heard
  action response(client: str, key: str, status: int, id: str): bool = all {
    answered' = if (200 <= status and status < 300) answered.put(key, answersOf(answered, key).union(Set(id))) else answered,
    requested' = requested, charges' = charges, captured' = captured,
  }

  action charge(key: str, id: str, account: str, amount: int): bool = all {
    charges' = charges.put(key, chargesOf(charges, key).union(Set((id, account, amount)))),
    requested' = requested, captured' = captured, answered' = answered,
  }

  action capture(key: str, amount: int): bool = all {
    captured' = captured.put(key, capturesOf(captured, key) + 1),
    requested' = requested, charges' = charges, answered' = answered,
  }

  action step = {
    nondet key = Set("k1", "k2").oneOf()
    nondet client = Set("c1", "c2").oneOf()
    nondet id = Set("ch_1", "ch_2").oneOf()
    nondet status = Set(0, 201, 502).oneOf()
    nondet amount = Set(100, 250).oneOf()
    any {
      request(client, key, "acct_1", amount),
      response(client, key, status, id),
      charge(key, id, "acct_1", amount),
      capture(key, amount),
    }
  }

  // a key is charged at most once
  val noDoubleCharge: bool = charges.keys().forall(k => charges.get(k).size() <= 1)

  // a key's money is captured at most once
  val noDoubleCapture: bool = captured.keys().forall(k => captured.get(k) <= 1)

  // every successful response for a key names one charge, the key's, once
  // the key has one
  val responseMatchesCharge: bool = answered.keys().forall(k =>
    answered.get(k).size() <= 1 and (idsOf(charges, k) == Set() or answered.get(k).subseteq(idsOf(charges, k))))

  // a charge is of what its key asked
  val chargeMatchesRequest: bool = charges.keys().forall(k =>
    requested.keys().contains(k) and charges.get(k).forall(c => (c._2, c._3) == requested.get(k)))

  // after the system settled: every key answered with success has exactly
  // one charge, the one its responses named, and exactly one capture
  val answeredCharged: bool = answered.keys().forall(k =>
    idsOf(charges, k).size() == 1 and answered.get(k) == idsOf(charges, k) and capturesOf(captured, k) == 1)

  // after the system settled: every key whose money the processor
  // captured was charged (a capture the service never recorded is money
  // taken for nothing)
  val capturedCharged: bool = captured.keys().forall(k => idsOf(charges, k).size() >= 1)
}
