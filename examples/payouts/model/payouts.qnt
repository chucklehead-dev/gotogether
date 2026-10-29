// The payouts app's invariants: what its bindings judge, over the generated
// store's and clients' actions (model/payouts_steps.qnt) and the app's rules (model/payouts_rules.qnt).
// Regenerating the skeleton never touches this file.

// The store's side (payouts.binding.yaml): mark a val `// grist: invariant`
// or `// grist: eventually` (after a settle) on the line before it, and
// regenerate; a binding name after the kind puts it in a test-only variant.
module payouts {
  import grist.* from "./grist"
  import payoutsEvents.* from "./payouts_gen"
  import payoutsRules.* from "./payouts_rules"
  import payoutsGen.* from "./payouts_steps"
  export payoutsGen.*

  // each payout is sent once: request refuses its id twice, and send's
  // success commits with its checkpoint
  // grist: invariant
  val oneSentPerPayout = log.indices().forall(i => log.indices().forall(j =>
    (i < j and log[i].typ == "sent" and log[j].typ == "sent") implies sentPayout(log[i]) != sentPayout(log[j])))
}

// The clients' side (payouts.calls.binding.yaml), linearized.
module payoutsCalls {
  import grist.* from "./grist"
  import payoutsEvents.* from "./payouts_gen"
  import payoutsRules.* from "./payouts_rules"
  import payoutsCallsGen.* from "./payouts_steps"
  export payoutsCallsGen.*
}
