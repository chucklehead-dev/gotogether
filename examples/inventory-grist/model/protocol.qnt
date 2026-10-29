// Independent business contract; no generated local-model imports.
module protocol {
  type State = { placeIDs: str -> str, cancelIDs: str -> str, confirmIDs: str -> str, placed: Set[str], cancelRequests: Set[str], confirmRequests: Set[str],
    reserveAnswers: Set[str], cancelAnswers: Set[str], confirmAnswers: Set[str],
    reserved: Set[str], held: Set[str], cancelled: Set[str], authorized: str -> str, started: Set[str] }
  var s: State
  action init = s' = { placeIDs: Map(), cancelIDs: Map(), confirmIDs: Map(), placed: Set(), cancelRequests: Set(), confirmRequests: Set(),
    reserveAnswers: Set(), cancelAnswers: Set(), confirmAnswers: Set(), reserved: Set(), held: Set(),
    cancelled: Set(), authorized: Map(), started: Set() }

  pure def names(ids: str -> str, id: str, order: str): bool = if (ids.keys().contains(id)) ids.get(id) == order else false

  val intentIDs = s.placeIDs.keys().union(s.cancelIDs.keys()).union(s.confirmIDs.keys())

  action checkoutPlace(id: str, order: str, result: str): bool = all {
    result != "ok" or (not(intentIDs.contains(id)) and not(s.placed.contains(order))),
    s' = if (result == "ok") { ...s, placed: s.placed.union(Set(order)), placeIDs: s.placeIDs.put(id,order) } else s,
  }
  action checkoutCancel(id: str, order: str, result: str): bool = all {
    result != "ok" or (not(intentIDs.contains(id)) and s.placed.contains(order) and not(s.cancelRequests.contains(order))),
    s' = if (result == "ok") { ...s, cancelRequests: s.cancelRequests.union(Set(order)), cancelIDs: s.cancelIDs.put(id,order) } else s,
  }
  action checkoutConfirm(id: str, order: str, result: str): bool = all {
    result != "ok" or (not(intentIDs.contains(id)) and s.placed.contains(order) and not(s.confirmRequests.contains(order))),
    s' = if (result == "ok") { ...s, confirmRequests: s.confirmRequests.union(Set(order)), confirmIDs: s.confirmIDs.put(id,order) } else s,
  }
  action inventoryReserve(order: str, result: str, parentApp: str, parentID: str): bool = {
    val expected = if (s.cancelled.contains(order)) "cancelled"
      else if (s.authorized.keys().map(k => s.authorized.get(k)).contains(order)) "too-late"
      else if (s.held.size() >= 2) "full" else "ok"
    all {
      parentApp == "checkout", names(s.placeIDs,parentID,order), s.placed.contains(order), result == expected or result == "conflict",
      result != "ok" or not(s.reserved.contains(order)),
      s' = if (result == "ok") { ...s, reserveAnswers: s.reserveAnswers.union(Set(parentID)),
        reserved: s.reserved.union(Set(order)), held: s.held.union(Set(order)) }
        else { ...s, reserveAnswers: s.reserveAnswers.union(Set(parentID)) },
    }
  }
  action inventoryCancel(order: str, result: str, parentApp: str, parentID: str): bool = {
    val expected = if (s.authorized.keys().map(k => s.authorized.get(k)).contains(order)) "too-late" else "ok"
    all {
      parentApp == "checkout", names(s.cancelIDs,parentID,order), s.cancelRequests.contains(order), result == expected or result == "conflict",
      s' = if (result == "ok") { ...s, cancelAnswers: s.cancelAnswers.union(Set(parentID)),
        cancelled: s.cancelled.union(Set(order)), held: s.held.exclude(Set(order)) }
        else { ...s, cancelAnswers: s.cancelAnswers.union(Set(parentID)) },
    }
  }
  action inventoryAuthorize(id: str, order: str, result: str, parentApp: str, parentID: str): bool = {
    val expected = if (s.held.contains(order) and not(s.cancelled.contains(order)) and not(s.authorized.keys().map(k => s.authorized.get(k)).contains(order))) "ok" else "not-held"
    all {
      parentApp == "checkout", names(s.confirmIDs,parentID,order), s.confirmRequests.contains(order), result == expected or result == "conflict",
      result != "ok" or not(s.authorized.keys().contains(id)),
      s' = if (result == "ok") { ...s, confirmAnswers: s.confirmAnswers.union(Set(parentID)), authorized: s.authorized.put(id,order) }
        else { ...s, confirmAnswers: s.confirmAnswers.union(Set(parentID)) },
    }
  }
  action fulfillmentStart(order: str, result: str, parentApp: str, parentID: str): bool = all {
    parentApp == "inventory", result == "ok",
    if (s.authorized.keys().contains(parentID)) s.authorized.get(parentID) == order else false,
    not(s.cancelled.contains(order)), not(s.started.contains(order)),
    s' = { ...s, started: s.started.union(Set(order)) },
  }
  val withinCapacity = s.held.size() <= 2
  val noCancelledShipment = s.cancelled.intersect(s.started).size() == 0
  val allIntentsAnswered = s.placeIDs.keys().subseteq(s.reserveAnswers) and s.cancelIDs.keys().subseteq(s.cancelAnswers) and s.confirmIDs.keys().subseteq(s.confirmAnswers)
  val allAuthorizedStarted = s.authorized.keys().map(k => s.authorized.get(k)).subseteq(s.started)

  // Finite inputs for model-only safety sampling and bounded verification.
  // Native histories still supply their actual ids and outcomes when checked.
  action step = {
    nondet id = Set("x", "y", "z", "w").oneOf()
    nondet order = Set("a", "b").oneOf()
    nondet parentID = Set("x", "y", "z", "w").oneOf()
    nondet result = Set("ok", "duplicate", "unknown-order", "cancelled", "too-late", "full", "not-held").oneOf()
    any {
      checkoutPlace(id,order,result), checkoutCancel(id,order,result), checkoutConfirm(id,order,result),
      inventoryReserve(order,result,"checkout",parentID), inventoryCancel(order,result,"checkout",parentID),
      inventoryAuthorize(id,order,result,"checkout",parentID), fulfillmentStart(order,result,"inventory",parentID),
    }
  }
}
