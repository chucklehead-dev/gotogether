// The shop's rules (model_test.go's shopApp): the holes of its generated
// skeleton (shop_steps.qnt), filled.
module shopRules {
  import grist.* from "./grist"
  import shopEvents.* from "./shop_gen"

  pure val STRS: Set[str] = Set("a", "b")
  pure val INTS: Set[int] = 1.to(2)

  pure def stock(log: List[Event], i: str): int = stockEvents(log, i).foldl(0, (s, e) => s + itemAddedQty(e))

  pure def addItemOutcome(log: List[Event], c: AddItemArgs): str = if (stock(log, c.item) + c.qty > 3) "full" else "ok"
  pure def addItemEvents(log: List[Event], c: AddItemArgs): List[Event] = [itemAdded(c.item, c.qty, false)]
  pure def addItemNeeds(c: AddItemArgs, result: str): Set[Read] = stockReads(c.item)

  // the open-shop rule: a shop takes four additions, read from items
  pure def openShopRefusal(log: List[Event], e: Event): str = if (itemsEvents(log).length() >= 4) "closed" else ""
  pure def openShopNeeds(e: Event, refusal: str): Set[Read] = itemsReads

  pure def noteItemOutcome(log: List[Event], c: NoteItemArgs): str = "ok"
  pure def noteItemEvents(log: List[Event], c: NoteItemArgs): List[Event] = [itemNoted]
  pure def noteItemNeeds(c: NoteItemArgs, result: str): Set[Read] = Set()

  pure def showItemAnswer(log: List[Event], q: ShowItemArgs): ShowItemResult = { qty: stock(log, q.item), shipped: false }
  pure def countAnswer(log: List[Event], q: CountArgs): int = itemsEvents(log).length()

  pure def shipEmits(prefix: List[Event], trigger: int): List[Event] = [itemShipped(itemAddedItem(prefix[trigger - 1]), trigger)]
  pure def notifySuccess(prefix: List[Event], trigger: int): List[Event] = [itemNoted]
  // notify skips gifts; charge skips nothing
  pure def notifySkips(prefix: List[Event], trigger: int): bool = itemAddedGift(prefix[trigger - 1])
  pure def chargeSkips(prefix: List[Event], trigger: int): bool = false
  // notify holds the added quantity of its item, charge one of the shipped
  pure def notifyWarehouseHold(prefix: List[Event], trigger: int): WarehouseHoldRequest =
    { item: itemAddedItem(prefix[trigger - 1]), qty: itemAddedQty(prefix[trigger - 1]) }
  pure def chargeWarehouseHold(prefix: List[Event], trigger: int): WarehouseHoldRequest =
    { item: itemShippedItem(prefix[trigger - 1]), qty: 1 }
  // invoice skips nothing, and bills one of the shipped item at billing
  pure def invoiceSkips(prefix: List[Event], trigger: int): bool = false
  pure def invoiceBillingBill(prefix: List[Event], trigger: int): BillingBillRequest =
    { item: itemShippedItem(prefix[trigger - 1]), qty: 1 }
  // the warehouse picks a key once
  pure def warehousePicksAdmits(records: List[WarehousePicksRecord], r: WarehousePicksRecord): bool =
    records.select(p => p.key == r.key).length() == 0
}
