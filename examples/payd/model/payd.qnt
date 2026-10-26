// A payments service's contract, as its clients, its database and its
// processor see it: an idempotency key is charged at most once and
// captured at most once, every successful response for a key is the key's
// one charge, a refusal of what the key asked means it was never charged,
// and money leaves an account only as its charges say. The steps:
//   - request: a client's attempt, as sent (its key, account and amount);
//   - response: what the attempt heard back (an HTTP status, 0 for no
//     response) and, for a 2xx, the charge it names (id, account, amount);
//   - charge: a charge the database committed, and chargeChanged one it
//     later updated or deleted;
//   - debit: money a commit took from an account (the key of the charge
//     it inserted with it, "" if none);
//   - capture: a capture the processor made, by the reference it was
//     given;
//   - balance: an account's balance, read once the system settled.
//
// The steps commute: a charge may be recorded before or after the
// response that names it (the database's commit is recorded where it
// commits, a response where it arrives, and a real run's charges are read
// from the table after it). So the properties are about what was seen,
// whatever its order, and what must have happened is judged after the
// system settled.
module payd {
  // each account's balance before any charge
  const INITIAL: str -> int

  // each key's request: its account and amount, as first asked
  var requested: str -> (str, int)
  // each key's charges: (id, account, amount); more than one is a double
  // charge
  var charges: str -> Set[(str, str, int)]
  // the ids of charges updated or deleted after their insert
  var changed: Set[str]
  // each account's debits, summed
  var debited: str -> int
  // each key's captures at the processor (by the reference it was given)
  var captured: str -> int
  // the charges each key's successful (2xx) responses named
  var answered: str -> Set[(str, str, int)]
  // the keys a 4xx refused when asked what they first asked
  var refused: Set[str]
  // the balances read after the system settled
  var observed: str -> int

  action init = all {
    requested' = Map(),
    charges' = Map(),
    changed' = Set(),
    debited' = Map(),
    captured' = Map(),
    answered' = Map(),
    refused' = Set(),
    observed' = Map(),
  }

  pure def chargesOf(cs: str -> Set[(str, str, int)], k: str): Set[(str, str, int)] =
    if (cs.keys().contains(k)) cs.get(k) else Set()
  pure def idsOf(cs: str -> Set[(str, str, int)], k: str): Set[str] = chargesOf(cs, k).map(c => c._1)
  pure def capturesOf(c: str -> int, k: str): int = if (c.keys().contains(k)) c.get(k) else 0
  pure def intOf(m: str -> int, k: str): int = if (m.keys().contains(k)) m.get(k) else 0
  // what an account's charges add up to
  pure def chargedTo(cs: str -> Set[(str, str, int)], a: str): int =
    cs.keys().fold(0, (s, k) => cs.get(k).fold(s, (t, c) => if (c._2 == a) t + c._3 else t))

  action request(client: str, key: str, account: str, amount: int): bool = all {
    requested' = if (requested.keys().contains(key)) requested else requested.put(key, (account, amount)),
    charges' = charges, changed' = changed, debited' = debited, captured' = captured,
    answered' = answered, refused' = refused, observed' = observed,
  }

  // account and amount are what the attempt asked; chargeAccount and
  // chargeAmount what a 2xx's charge says. Status 0 is no response (a
  // timeout, a reset, a refusal): nothing heard.
  action response(client: str, key: str, account: str, amount: int, status: int, id: str, chargeAccount: str, chargeAmount: int): bool = all {
    answered' = if (200 <= status and status < 300)
      answered.put(key, chargesOf(answered, key).union(Set((id, chargeAccount, chargeAmount))))
      else answered,
    refused' = if (400 <= status and status < 500 and requested.keys().contains(key) and requested.get(key) == (account, amount))
      refused.union(Set(key))
      else refused,
    requested' = requested, charges' = charges, changed' = changed, debited' = debited, captured' = captured, observed' = observed,
  }

  action charge(key: str, id: str, account: str, amount: int): bool = all {
    charges' = charges.put(key, chargesOf(charges, key).union(Set((id, account, amount)))),
    requested' = requested, changed' = changed, debited' = debited, captured' = captured,
    answered' = answered, refused' = refused, observed' = observed,
  }

  action chargeChanged(id: str): bool = all {
    changed' = changed.union(Set(id)),
    requested' = requested, charges' = charges, debited' = debited, captured' = captured,
    answered' = answered, refused' = refused, observed' = observed,
  }

  action debit(account: str, key: str, amount: int): bool = all {
    debited' = debited.put(account, intOf(debited, account) + amount),
    requested' = requested, charges' = charges, changed' = changed, captured' = captured,
    answered' = answered, refused' = refused, observed' = observed,
  }

  action capture(key: str, amount: int): bool = all {
    captured' = captured.put(key, capturesOf(captured, key) + 1),
    requested' = requested, charges' = charges, changed' = changed, debited' = debited,
    answered' = answered, refused' = refused, observed' = observed,
  }

  action balance(account: str, balance: int): bool = all {
    observed' = observed.put(account, balance),
    requested' = requested, charges' = charges, changed' = changed, debited' = debited,
    captured' = captured, answered' = answered, refused' = refused,
  }

  action step = {
    nondet key = Set("k1", "k2").oneOf()
    nondet client = Set("c1", "c2").oneOf()
    nondet id = Set("ch_1", "ch_2").oneOf()
    nondet status = Set(0, 201, 409, 502).oneOf()
    nondet amount = Set(100, 250).oneOf()
    nondet account = INITIAL.keys().oneOf()
    nondet b = Set(0, 750, 900).oneOf()
    any {
      request(client, key, account, amount),
      response(client, key, account, amount, status, id, account, amount),
      charge(key, id, account, amount),
      chargeChanged(id),
      debit(account, key, amount),
      capture(key, amount),
      balance(account, b),
    }
  }

  // a key is charged at most once
  val noDoubleCharge: bool = charges.keys().forall(k => charges.get(k).size() <= 1)

  // a key's money is captured at most once
  val noDoubleCapture: bool = captured.keys().forall(k => captured.get(k) <= 1)

  // every successful response for a key names one charge, the key's (its
  // id, account and amount), once the key has one
  val responseMatchesCharge: bool = answered.keys().forall(k =>
    answered.get(k).size() <= 1 and (chargesOf(charges, k) == Set() or answered.get(k).subseteq(chargesOf(charges, k))))

  // a charge is of what its key asked
  val chargeMatchesRequest: bool = charges.keys().forall(k =>
    requested.keys().contains(k) and charges.get(k).forall(c => (c._2, c._3) == requested.get(k)))

  // a key refused (4xx) when it asked what it first asked is never
  // charged, nor captured: a client told no was not charged
  val noRefusalOfCharged: bool = refused.forall(k => chargesOf(charges, k) == Set() and capturesOf(captured, k) == 0)

  // a charge, once made, stays as it was
  val chargesImmutable: bool = changed == Set()

  // no account loses more than its charges add up to (a commit records
  // its charge before its debit)
  val noDebitWithoutCharge: bool = debited.keys().forall(a => debited.get(a) <= chargedTo(charges, a))

  // a balance read after the settle is the initial one less the
  // account's charges
  val balanceMatchesCharges: bool = observed.keys().forall(a =>
    INITIAL.keys().contains(a) and observed.get(a) == INITIAL.get(a) - chargedTo(charges, a))

  // after the system settled: every key answered with success has exactly
  // one charge, the one its responses named, and exactly one capture
  val answeredCharged: bool = answered.keys().forall(k =>
    chargesOf(charges, k).size() == 1 and answered.get(k) == chargesOf(charges, k) and capturesOf(captured, k) == 1)

  // after the system settled: every key whose money the processor
  // captured was charged (a capture the service never recorded is money
  // taken for nothing)
  val capturedCharged: bool = captured.keys().forall(k => idsOf(charges, k).size() >= 1)

  // after the system settled: every key asked for validly (an account
  // there is, a positive amount) was answered with success: the client
  // retries until it is, and the faults stop at the settle
  val allAnswered: bool = requested.keys().forall(k =>
    not(INITIAL.keys().contains(requested.get(k)._1) and requested.get(k)._2 > 0) or chargesOf(answered, k) != Set())
}
