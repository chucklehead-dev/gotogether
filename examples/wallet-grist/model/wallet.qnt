// examples/wallet-grist: examples/wallet's bank on grist, as one tenant's
// log: the invariants its bindings judge, over grist's generated actions
// (wallet_steps.qnt, `wallet model`: the actions of every block, the frame
// conditions, step) and the app's rules (wallet_rules.qnt).
// examples/wallet/model/wallet.qnt keeps
// balances, each request's outcome and its moves as variables; here they
// are defs over the log, each request's outcome is grist's marker, and the
// ledger's posts are the post processor's effect steps, its calls of the
// ledger (ledgerPost) and the ledger's own record of what it posted
// (ledgerPostServed).

// The store's side (wallet.binding.yaml and its variant).
module wallet {
  import grist.* from "./grist"
  import walletEvents.* from "./wallet_gen"
  import walletRules.* from "./wallet_rules"
  import walletGen.* from "./wallet_steps"
  export walletGen.*

  // the design's: no overdraft, in any state
  // grist: invariant
  val noOverdraft = noOverdraftIn(log)

  // every move posted (an ok effect step, checkpointed), or its post
  // failed every try and says so, is the catalog's obligation, generated in
  // wallet_steps.qnt (postEventuallyHandled, with ledgerPostEventuallyServed)

  // every post the ledger made is a move's, under that move's effect key
  // (the ledger records before its answer is the effect's step, so this
  // is judged after the settle)
  // grist: eventually
  val postedApplied =
    ledgerPostRecords.foldl(true, (b, p) => b and effects.indices().exists(j => and {
      effects[j]._1 == "post",
      effects[j]._3 == p.key,
      movedAccount(log[effects[j]._2 - 1]) == p.request.account,
      movedAmount(log[effects[j]._2 - 1]) == p.request.amount,
    }))

  // test-only: the ledger holds every move exactly once, under its effect
  // key. At least once allows a post given up after EFFECT_TRIES failures
  // (grist.failed); a run without ledger faults must not have one.
  // grist: eventually exactly
  val everyMovePostedOnce =
    log.indices().forall(i => log[i].typ == "moved" implies
      effects.indices().exists(j => effects[j]._1 == "post" and effects[j]._2 == i + 1 and
        ledgerPostRecords.select(p => p.key == effects[j]._3).length() == 1))
}

// The clients' side (wallet.calls.binding.yaml), linearized.
module walletCalls {
  import grist.* from "./grist"
  import walletEvents.* from "./wallet_gen"
  import walletRules.* from "./wallet_rules"
  import walletCallsGen.* from "./wallet_steps"
  export walletCallsGen.*

  // grist: invariant
  val noOverdraft = noOverdraftIn(log)
}
