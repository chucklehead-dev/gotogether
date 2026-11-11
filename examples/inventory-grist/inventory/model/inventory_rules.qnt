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
  // The units held: the sum of stock's values, written as a count of the
  // reservations no later cancellation of their order clears. The two are
  // equal on every log (stock puts 0 at a cancellation and adds 1 at each
  // reservation); the count keeps quint verify's encoding linear in the
  // log, where a map built by a fold and folded over its keys made each
  // step's SMT query grow about tenfold (docs/performance.md, J7b).
  pure def used(log: List[Event]): int = { val st = stockEvents(log)
    st.indices().filter(i => st[i].typ == "reserved" and st.indices().forall(j =>
      j <= i or not(st[j].typ == "cancelled" and cancelledOrder(st[j]) == reservedOrder(st[i])))).size() }
  // Model the public query from this order's event history, independently of
  // the Go read model. Authorization keeps its units; cancellation clears them.
  pure def allocationAnswer(log: List[Event], q: AllocationArgs): AllocationResult = {
    units: allocationsEvents(log,q.order).foldl(0, (n,e) =>
      if (e.typ == "reserved") n + 1 else if (e.typ == "cancelled") 0 else n),
    status: status(log,q.order)
  }
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
