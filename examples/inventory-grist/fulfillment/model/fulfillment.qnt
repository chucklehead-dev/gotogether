// The fulfillment app's invariants: what its bindings judge, over the generated
// store's and clients' actions (model/fulfillment_steps.qnt) and the app's rules (model/fulfillment_rules.qnt).
// Regenerating the skeleton never touches this file.

// The store's side (fulfillment.binding.yaml): mark a val `// grist: invariant`
// or `// grist: eventually` (after a settle) on the line before it, and
// regenerate; a binding name after the kind puts it in a test-only variant.
module fulfillment {
  import grist.* from "./grist"
  import fulfillmentEvents.* from "./fulfillment_gen"
  import fulfillmentRules.* from "./fulfillment_rules"
  import fulfillmentGen.* from "./fulfillment_steps"
  export fulfillmentGen.*
  // A caller may ask twice under distinct keys; the composed checkout must not.
  // grist: violable
  val oneShipmentPerOrder = log.indices().forall(i => log.indices().forall(j =>
    (i < j and log[i].typ == "started" and log[j].typ == "started") implies startedOrder(log[i]) != startedOrder(log[j])))
}

// The clients' side (fulfillment.calls.binding.yaml), linearized.
module fulfillmentCalls {
  import grist.* from "./grist"
  import fulfillmentEvents.* from "./fulfillment_gen"
  import fulfillmentRules.* from "./fulfillment_rules"
  import fulfillmentCallsGen.* from "./fulfillment_steps"
  export fulfillmentCallsGen.*
}
