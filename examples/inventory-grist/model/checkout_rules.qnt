module checkoutRules {
  import grist.* from "./grist"
  import checkoutEvents.* from "./checkout_gen"
  pure val STRS: Set[str] = Set("a", "b", "c", "d")
  pure val INTS: Set[int] = Set(1, 2)
  pure def has(log: List[Event], order: str, typ: str): bool = ordersEvents(log, order).select(e => e.typ == typ).length() > 0
  pure def placeOutcome(log: List[Event], c: PlaceArgs): str = if (has(log,c.order,"placed")) "duplicate" else "ok"
  pure def placeEvents(log: List[Event], c: PlaceArgs): List[Event] = [placed(c.order)]
  pure def placeNeeds(c: PlaceArgs, result: str): Set[Read] = ordersReads(c.order)
  pure def cancelOutcome(log: List[Event], c: CancelArgs): str =
    if (not(has(log,c.order,"placed"))) "unknown-order" else if (has(log,c.order,"cancellation-requested")) "duplicate" else "ok"
  pure def cancelEvents(log: List[Event], c: CancelArgs): List[Event] = [cancellationRequested(c.order)]
  pure def cancelNeeds(c: CancelArgs, result: str): Set[Read] = ordersReads(c.order)
  pure def confirmOutcome(log: List[Event], c: ConfirmArgs): str =
    if (not(has(log,c.order,"placed"))) "unknown-order" else if (has(log,c.order,"confirmation-requested")) "duplicate" else "ok"
  pure def confirmEvents(log: List[Event], c: ConfirmArgs): List[Event] = [confirmationRequested(c.order)]
  pure def confirmNeeds(c: ConfirmArgs, result: str): Set[Read] = ordersReads(c.order)
  pure def reserveStockSkips(prefix: List[Event], trigger: int): bool = false
  pure def cancelStockSkips(prefix: List[Event], trigger: int): bool = false
  pure def confirmStockSkips(prefix: List[Event], trigger: int): bool = false
  pure def reserveStockInventoryReserve(prefix: List[Event], trigger: int): InventoryReserveRequest = { order: placedOrder(prefix[trigger-1]) }
  pure def cancelStockInventoryCancel(prefix: List[Event], trigger: int): InventoryCancelRequest = { order: cancellationRequestedOrder(prefix[trigger-1]) }
  pure def confirmStockInventoryAuthorize(prefix: List[Event], trigger: int): InventoryAuthorizeRequest = { order: confirmationRequestedOrder(prefix[trigger-1]) }
}
