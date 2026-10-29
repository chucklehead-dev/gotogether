// The inventory app's invariants: what its bindings judge, over the generated
// store's and clients' actions (model/inventory_steps.qnt) and the app's rules (model/inventory_rules.qnt).
// Regenerating the skeleton never touches this file.

// The store's side (inventory.binding.yaml): mark a val `// grist: invariant`
// or `// grist: eventually` (after a settle) on the line before it, and
// regenerate; a binding name after the kind puts it in a test-only variant.
module inventory {
  import grist.* from "./grist"
  import inventoryEvents.* from "./inventory_gen"
  import inventoryRules.* from "./inventory_rules"
  import inventoryGen.* from "./inventory_steps"
  export inventoryGen.*
  // grist: invariant
  val withinCapacity = used(log) <= 2
}

// The clients' side (inventory.calls.binding.yaml), linearized.
module inventoryCalls {
  import grist.* from "./grist"
  import inventoryEvents.* from "./inventory_gen"
  import inventoryRules.* from "./inventory_rules"
  import inventoryCallsGen.* from "./inventory_steps"
  export inventoryCallsGen.*
}
