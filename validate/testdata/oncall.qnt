// Charges captured and refunded, each answer the request's result
// (oncall_test.go): what an on-call report says of a refund past its
// capture (refundsWithinCapture) and of a capture the code refused that
// the model allows.
module oncall {
  const IDS: Set[str]
  const AMOUNTS: Set[int]
  type Charge = { captured: int, refunded: int, status: str }
  var charges: str -> Charge

  action init = charges' = Map()

  action authorize(id: str, result: str): bool = all {
    not(charges.keys().contains(id)), result == "ok",
    charges' = charges.put(id, { captured: 0, refunded: 0, status: "authorized" }),
  }
  action capture(id: str, amount: int, result: str): bool = all {
    charges.keys().contains(id),
    if (charges.get(id).status == "authorized")
      all { result == "ok", charges' = charges.set(id, { ...charges.get(id), captured: amount, status: "captured" }) }
    else
      all { result == "invalid_state", charges' = charges },
  }
  // the model takes any refund of a known charge: the code's to keep it
  // within the capture
  action refund(id: str, amount: int, result: str): bool = all {
    charges.keys().contains(id), result == "ok",
    charges' = charges.setBy(id, c => { ...c, refunded: c.refunded + amount }),
  }

  action step = {
    nondet id = IDS.oneOf()
    nondet a = AMOUNTS.oneOf()
    any { authorize(id, "ok"), capture(id, a, "ok"), capture(id, a, "invalid_state"), refund(id, a, "ok") }
  }

  val refundsWithinCapture = charges.keys().forall(i => charges.get(i).refunded <= charges.get(i).captured)
  val capturedAuthorized = charges.keys().forall(i => charges.get(i).status != "authorized" or charges.get(i).captured == 0)
}
