// The shop's invariants (model_test.go's shopApp), over its generated
// steps (shop_steps.qnt) and its rules (shop_rules.qnt).

module shop {
  import grist.* from "./grist"
  import shopEvents.* from "./shop_gen"
  import shopRules.* from "./shop_rules"
  import shopGen.* from "./shop_steps"
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
  import shopRules.* from "./shop_rules"
  import shopCallsGen.* from "./shop_steps"
  export shopCallsGen.*

  // grist: invariant
  val stockBounded = STRS.forall(i => stock(log, i) <= 3)
}
