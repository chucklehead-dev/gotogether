module inventoryRules {
  import grist.* from "./grist"
  import inventoryEvents.* from "./inventory_gen"
  pure val STRS: Set[str] = Set("a", "b", "c", "d")
  pure val INTS: Set[int] = Set(1, 2)
  pure def status(log: List[Event], order: str): str = allocationsEvents(log,order).foldl("none", (s,e) =>
    if (e.typ == "reserved") "held" else if (e.typ == "cancelled") "cancelled" else "authorized")
  pure def stock(log: List[Event]): str -> int = stockEvents(log).foldl(Map(), (s,e) =>
    if (e.typ == "cancelled") s.put(cancelledOrder(e), 0)
    else { val k = reservedOrder(e)
      s.put(k, 1 + (if (s.keys().contains(k)) s.get(k) else 0)) })
  pure def used(log: List[Event]): int = { val s = stock(log)
    s.keys().fold(0, (n,k) => n + s.get(k)) }
  pure def reserveOutcome(log: List[Event], c: ReserveArgs): str =
    if (status(log,c.order) == "cancelled") "cancelled"
    else if (status(log,c.order) == "authorized") "too-late"
    else if (used(log) >= 2) "full" else "ok"
  pure def reserveEvents(log: List[Event], c: ReserveArgs): List[Event] = [reserved(c.order)]
  pure def reserveNeeds(c: ReserveArgs, result: str): Set[Read] =
    if (Set("cancelled","too-late").contains(result)) allocationsReads(c.order)
    else allocationsReads(c.order).union(stockReads)
  pure def cancelOutcome(log: List[Event], c: CancelArgs): str = if (status(log,c.order) == "authorized") "too-late" else "ok"
  pure def cancelEvents(log: List[Event], c: CancelArgs): List[Event] = [cancelled(c.order)]
  pure def cancelNeeds(c: CancelArgs, result: str): Set[Read] = allocationsReads(c.order)
  pure def authorizeOutcome(log: List[Event], c: AuthorizeArgs): str = if (status(log,c.order) == "held") "ok" else "not-held"
  pure def authorizeEvents(log: List[Event], c: AuthorizeArgs): List[Event] = [authorized(c.order)]
  pure def authorizeNeeds(c: AuthorizeArgs, result: str): Set[Read] = allocationsReads(c.order)
  pure def shipSkips(prefix: List[Event], trigger: int): bool = false
  pure def shipFulfillmentStart(prefix: List[Event], trigger: int): FulfillmentStartRequest = { order: authorizedOrder(prefix[trigger-1]) }
}
