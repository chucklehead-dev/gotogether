// -*- mode: Bluespec; -*-
// The stage-3 draft of model/payments.qnt, kept to show what the
// migration's first checks found in it (README.md, stages 3 and 4): it
// was written from the code's comments and the tests alone.
// - capture takes the whole authorization (capture's doc comment says
//   so): simulate finds R3's partial capture impossible, and the legacy
//   test TestPartialCapture's run is rejected. The model's mistake.
// - authorize knows no idempotency keys (no test retries one): the
//   production log's replayed authorization is rejected. An undocumented
//   requirement, R2.
// - reads are left out: lint -src names lookup, which auditsteps records,
//   as no action of the draft.
module payments {
  // the charge ids, idempotency keys and amounts of a run
  const IDS: Set[str]
  const KEYS: Set[str]
  const AMOUNTS: Set[int]

  type Charge = { key: str, authorized: int, captured: int, refunded: int, status: str }

  var charges: str -> Charge

  // every result an operation answers: ok, or a refusal's name
  pure val RESULTS = Set("ok", "invalid", "not_found", "invalid_state", "exceeds")

  action init = charges' = Map()

  def known(id: str): bool = charges.keys().contains(id)
  def remaining(id: str): int = charges.get(id).captured - charges.get(id).refunded
  // a refusal: the result names it, and nothing changes
  action refused(result: str, name: str): bool = all { result == name, charges' = charges }

  action authorize(id: str, key: str, amount: int, result: str): bool = all {
    if (amount <= 0) refused(result, "invalid")
    else all {
      result == "ok",
      not(known(id)),
      charges' = charges.put(id, { key: key, authorized: amount, captured: 0, refunded: 0, status: "authorized" }),
    },
  }

  action capture(id: str, amount: int, result: str): bool = all {
    if (not(known(id))) refused(result, "not_found")
    else if (amount <= 0) refused(result, "invalid")
    else if (charges.get(id).status != "authorized") refused(result, "invalid_state")
    else if (amount != charges.get(id).authorized) refused(result, "exceeds") // "captures the authorization"
    else all { result == "ok", charges' = charges.setBy(id, c => { ...c, captured: amount, status: "captured" }) },
  }

  action refund(id: str, amount: int, result: str): bool = all {
    if (not(known(id))) refused(result, "not_found")
    else if (amount <= 0) refused(result, "invalid")
    else if (charges.get(id).status != "captured") refused(result, "invalid_state")
    else if (result == "exceeds") all { amount > remaining(id), charges' = charges }
    else all { result == "ok", charges' = charges.setBy(id, c => { ...c, refunded: c.refunded + amount }) },
  }

  action void(id: str, result: str): bool = all {
    if (not(known(id))) refused(result, "not_found")
    else if (charges.get(id).status != "authorized") refused(result, "invalid_state")
    else all { result == "ok", charges' = charges.setBy(id, c => { ...c, status: "voided" }) },
  }

  action step = {
    nondet id = IDS.oneOf()
    nondet key = KEYS.oneOf()
    nondet amount = AMOUNTS.oneOf()
    nondet result = RESULTS.oneOf()
    any { authorize(id, key, amount, result), capture(id, amount, result), refund(id, amount, result), void(id, result) }
  }

  // refunds never pass the capture: the code's to hold
  val refundsWithinCapture = charges.keys().forall(c => charges.get(c).refunded <= charges.get(c).captured)
  val captureWithinAuthorization = charges.keys().forall(c => charges.get(c).captured <= charges.get(c).authorized)
}
