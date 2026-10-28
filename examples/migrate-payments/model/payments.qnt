// -*- mode: Bluespec; -*-
// Context: payments, the legacy charge handler (owner: the payments team)
// State: charges, one row each (payments.go's Charge: its idempotency key,
//   authorized, captured and refunded amounts, status)
// Operations: authorize (POST /charges), capture (POST /charges/{id}/capture),
//   refund (POST /charges/{id}/refund), void (POST /charges/{id}/void),
//   lookup (GET /charges/{id})
// Environment: the store, one lock around each request (serial: the
//   model's steps are the requests in the order they took the lock, the
//   audit log's order)
// Outside: the merchant's notifications (a webhook after each capture,
//   fire and forget: the "notify merchant" spans, dropped by
//   payments.convert.yaml); the card network (no call in this service)
//
// Each requirement says where it was found (code, test, log, trace,
// owner) and, if it rests on one source, that it is assumed. The README's
// stages say how each was found and what each disagreement decided.
module payments {
  // the charge ids, idempotency keys ("" is none) and amounts of a run
  const IDS: Set[str]
  const KEYS: Set[str]
  const AMOUNTS: Set[int]

  type Charge = { key: str, authorized: int, captured: int, refunded: int, status: str }

  var charges: str -> Charge

  // every result an operation answers: ok, or a refusal's name
  pure val RESULTS = Set("ok", "replayed", "invalid", "not_found", "invalid_state", "exceeds")

  action init = charges' = Map()

  def known(id: str): bool = charges.keys().contains(id)
  // the charge a key already made (none for the empty key)
  def keyed(key: str): Set[str] = charges.keys().filter(c => key != "" and charges.get(c).key == key)
  def remaining(id: str): int = charges.get(id).captured - charges.get(id).refunded
  // a refusal: the result names it, and nothing changes
  action refused(result: str, name: str): bool = all { result == name, charges' = charges }

  // req R1: an authorization of a positive amount creates an authorized
  //   charge; any other amount is refused (invalid).
  //   code: charge.go authorize; test: payments_test.go TestAuthorize, TestAuthorizeInvalid;
  //   log: payments-2026-10-02.jsonl audit_seq 1
  // req R2: a key already used answers with its charge (replayed) and
  //   creates none.
  //   code: charge.go authorize (byKey); log: payments-2026-10-02.jsonl audit_seq 2 (a client retry)
  //   decided: an undocumented requirement, no test states it; the draft
  //   model rejected the log's replay (owner, 2026-10-28). Mutant replayCreatesCharge.
  action authorize(id: str, key: str, amount: int, result: str): bool = all {
    if (amount <= 0) refused(result, "invalid")
    else if (keyed(key) != Set()) all { result == "replayed", keyed(key).contains(id), charges' = charges }
    else all {
      result == "ok",
      not(known(id)),
      charges' = charges.put(id, { key: key, authorized: amount, captured: 0, refunded: 0, status: "authorized" }),
    },
  }

  // req R3: a capture takes at most the authorized amount, once; the rest
  //   is released.
  //   code: charge.go capture (amount > c.Authorized); test: TestPartialCapture, TestCaptureTooMuch;
  //   log: payments-2026-10-02.jsonl audit_seq 4 (600 of 1000)
  //   decided: the draft model captured the whole authorization, from
  //   capture's stale doc comment; simulate found partial captures
  //   impossible and TestPartialCapture's run was rejected: the model's
  //   mistake (owner, 2026-10-28).
  // req R4: a charge already captured, or voided, is not captured (invalid_state).
  //   code: charge.go capture (c.Status != "authorized"); test: TestCaptureTwice
  action capture(id: str, amount: int, result: str): bool = all {
    if (not(known(id))) refused(result, "not_found")
    else if (amount <= 0) refused(result, "invalid")
    else if (charges.get(id).status != "authorized") refused(result, "invalid_state")
    else if (amount > charges.get(id).authorized) refused(result, "exceeds")
    else all { result == "ok", charges' = charges.setBy(id, c => { ...c, captured: amount, status: "captured" }) },
  }

  // req R5: refunds of a charge add up to at most its capture.
  //   code: refund.go (each refund checked against the capture);
  //   test: TestRefundTooMuch, TestPartialRefund;
  //   log: payments-2026-10-02.jsonl audit_seq 10-11, ch_3 refunded 700 and 500 of 1000
  //   decided: the code's bug, found by the log (the legacy check forgot
  //   earlier refunds; no test refunds twice). Fixed in refund.go; the
  //   legacy check is mutant forgetsPriorRefunds (owner, 2026-10-28).
  //   The model takes the code's decision to refund and judges it with
  //   refundsWithinCapture, so a run that breaks R5 is reported as R5. A
  //   refusal (exceeds) must be justified.
  // req R9: a charge not captured is not refunded (invalid_state).
  //   code: refund.go (c.Status != "captured"); test: TestRefundBeforeCapture
  action refund(id: str, amount: int, result: str): bool = all {
    if (not(known(id))) refused(result, "not_found")
    else if (amount <= 0) refused(result, "invalid")
    else if (charges.get(id).status != "captured") refused(result, "invalid_state")
    else if (result == "exceeds") all { amount > remaining(id), charges' = charges }
    else all { result == "ok", charges' = charges.setBy(id, c => { ...c, refunded: c.refunded + amount }) },
  }

  // req R6: only an authorized charge is voided; a captured one is refused (invalid_state).
  //   code: charge.go void; test: TestVoid, TestVoidAfterCapture
  action void(id: str, result: str): bool = all {
    if (not(known(id))) refused(result, "not_found")
    else if (charges.get(id).status != "authorized") refused(result, "invalid_state")
    else all { result == "ok", charges' = charges.setBy(id, c => { ...c, status: "voided" }) },
  }

  // req R7: a lookup answers with the charge as it is (the binding
  //   compares its status and refunded amount).
  //   code: charge.go lookup; test: TestPartialCapture, TestLookupUnknown;
  //   log: payments-2026-10-02.jsonl audit_seq 13
  //   decided: the draft left reads out; lint -src named lookup, recorded
  //   by auditsteps, as no action of the draft: modelled, since a read is
  //   what a client acts on (owner, 2026-10-28).
  action lookup(id: str, result: str): bool = all {
    result == (if (known(id)) "ok" else "not_found"),
    charges' = charges,
  }

  action step = {
    nondet id = IDS.oneOf()
    nondet key = KEYS.oneOf()
    nondet amount = AMOUNTS.oneOf()
    nondet result = RESULTS.oneOf()
    any { authorize(id, key, amount, result), capture(id, amount, result), refund(id, amount, result), void(id, result), lookup(id, result) }
  }

  // R5 (above): refunds never pass the capture. In simulate's allows: the
  // model leaves the decision to the code, which must hold it.
  val refundsWithinCapture = charges.keys().forall(c => charges.get(c).refunded <= charges.get(c).captured)
  // R3 (above): a capture never passes its authorization.
  val captureWithinAuthorization = charges.keys().forall(c => charges.get(c).captured <= charges.get(c).authorized)
  // R2 (above): one key, one charge.
  val oneChargePerKey = charges.keys().forall(a => charges.keys().forall(b =>
    a == b or charges.get(a).key == "" or charges.get(a).key != charges.get(b).key))
  // req R8: a voided charge has nothing captured.
  //   code: charge.go capture (c.Status != "authorized" refuses a voided charge)
  //   assumed: no test voids then captures, no log line shows it; for the owner to confirm.
  val voidedNothingCaptured = charges.keys().forall(c => charges.get(c).status != "voided" or charges.get(c).captured == 0)
}
