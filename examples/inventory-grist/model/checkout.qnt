// The checkout app's invariants: what its bindings judge, over the generated
// store's and clients' actions (model/checkout_steps.qnt) and the app's rules (model/checkout_rules.qnt).
// Regenerating the skeleton never touches this file.

// The store's side (checkout.binding.yaml): mark a val `// grist: invariant`
// or `// grist: eventually` (after a settle) on the line before it, and
// regenerate; a binding name after the kind puts it in a test-only variant.
module checkout {
  import grist.* from "./grist"
  import checkoutEvents.* from "./checkout_gen"
  import checkoutRules.* from "./checkout_rules"
  import checkoutGen.* from "./checkout_steps"
  export checkoutGen.*
}

// The clients' side (checkout.calls.binding.yaml), linearized.
module checkoutCalls {
  import grist.* from "./grist"
  import checkoutEvents.* from "./checkout_gen"
  import checkoutRules.* from "./checkout_rules"
  import checkoutCallsGen.* from "./checkout_steps"
  export checkoutCallsGen.*
}
