// The bank app's invariants: what its bindings judge, over the generated
// store's and clients' actions (model/bank_steps.qnt) and the app's rules (model/bank_rules.qnt).
// Regenerating the skeleton never touches this file.

// The store's side (bank.binding.yaml): mark a val `// grist: invariant`
// or `// grist: eventually` (after a settle) on the line before it, and
// regenerate; a binding name after the kind puts it in a test-only variant.
module bank {
  import grist.* from "./grist"
  import bankEvents.* from "./bank_gen"
  import bankRules.* from "./bank_rules"
  import bankGen.* from "./bank_steps"
  export bankGen.*

  // no payout is paid twice: what the payouts service's at-least-once send
  // relies on the bank's markers for (every try one command, under its
  // effect's key). A caller sending each try under a new id
  // (gristtest.PeerMutants' freshKey) pays twice after a lost answer.
  // Violable: the bank on its own pays whatever its caller asks under a
  // new id, so it holds of the pair, not of the bank alone (the model on
  // its own reaches a violation: simulate's allows)
  // grist: violable
  val onePaymentPerPayout = log.indices().forall(i => log.indices().forall(j =>
    (i < j and log[i].typ == "paid" and log[j].typ == "paid") implies paidPayout(log[i]) != paidPayout(log[j])))
}

// The clients' side (bank.calls.binding.yaml), linearized.
module bankCalls {
  import grist.* from "./grist"
  import bankEvents.* from "./bank_gen"
  import bankRules.* from "./bank_rules"
  import bankCallsGen.* from "./bank_steps"
  export bankCallsGen.*
}
