// The shop's rules and invariants (model_test.go's shopApp): the holes
// of its generated skeleton, filled.
module shopRules {
  import grist.* from "./grist"
  import shopEvents.* from "./shop_gen"

  pure val STRS: Set[str] = Set("a", "b")
  pure val INTS: Set[int] = 1.to(2)

  pure def stock(log: List[Event], i: str): int = stockEvents(log, i).foldl(0, (s, e) => s + itemAddedQty(e))

  pure def addItemOutcome(log: List[Event], c: AddItemArgs): str = if (stock(log, c.item) + c.qty > 3) "full" else "ok"
  pure def addItemEvents(log: List[Event], c: AddItemArgs): List[Event] = [itemAdded(c.item, c.qty, false)]
  pure def addItemNeeds(c: AddItemArgs, result: str): Set[Read] = stockReads(c.item)

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
  // the warehouse picks a key once
  pure def warehousePicksAdmits(records: List[WarehousePicksRecord], r: WarehousePicksRecord): bool =
    records.select(p => p.key == r.key).length() == 0
}

module shop {
  import grist.* from "./grist"
  import shopEvents.* from "./shop_gen"
  import shopRules.*
  import shopGen.* from "./shop_gen"
  export shopGen.*

  // no item's stock passes 3
  // grist: invariant default audit
  val stockBounded = STRS.forall(i => stock(log, i) <= 3)

  // every addition shipped, once settled
  // grist: eventually
  val allShipped = checkpoints.get("ship") == log.length() or nextOf(log, checkpoints.get("ship"), "item-added") == 0

  // test-only: every pick's item was added
  // grist: violable audit
  val picksAdded = warehousePicksRecords.foldl(true, (b, r) => b and stock(log, r.item) > 0)
}

module shopCalls {
  import grist.* from "./grist"
  import shopEvents.* from "./shop_gen"
  import shopRules.*
  import shopCallsGen.* from "./shop_gen"
  export shopCallsGen.*

  // grist: invariant
  val stockBounded = STRS.forall(i => stock(log, i) <= 3)
}
