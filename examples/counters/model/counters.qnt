// examples/counters, grain's counter example on grist, as one tenant's
// log (docs/grist.md §8.1): the invariants its bindings judge, over grist's
// generated actions (counters_steps.qnt, `counters model`: the actions of
// every block, the frame conditions, step) and the app's rules
// (counters_rules.qnt).

// The store's side (counters.binding.yaml and its variants).
module counters {
  import grist.* from "./grist"
  import countersEvents.* from "./counters_gen"
  import countersRules.* from "./counters_rules"
  import countersGen.* from "./counters_steps"
  export countersGen.*

  // grist: invariant default retiring real
  val uniqueNames = namesUnique(log)

  // each average-computed is the sum and count of the increments through
  // its trigger: the k-th is the k-th increment's prefix
  // grist: invariant default retiring real
  val averageAsOfTrigger =
    val sums = log.foldl([], (acc, e) =>
      if (e.typ == "counter-incremented") acc.append(if (acc.length() == 0) e.ints[0] else acc[acc.length() - 1] + e.ints[0])
      else acc)
    val avgs = log.select(e => e.typ == "average-computed")
    avgs.indices().forall(k => k < sums.length() and avgs[k].ints == [sums[k], k + 1])

  // Every created counter welcomed (or its welcome failed every try and
  // says so) and every retirement's payout checkpointed are the catalog's
  // obligations, generated in counters_steps.qnt (welcomeEventuallyHandled,
  // payoutEventuallyHandled, with averageEventuallyHandled and
  // bankPayEventuallyServed) and judged by the default binding and the
  // retiring and real variants: what follows says more.

  // Site claims: every answer a caller got is one its dependency gave
  // (its served record of the call, request, outcome and response alike,
  // a replay's being an earlier one's), or unknown. They need the
  // dependency's site recorded: in every simulation (gristsim serves
  // both), and on the real tier for the bank only, which runs behind
  // grist.ServeCalls with its own step file; the real tier's mailer is a
  // third party's, which records nothing, so the real variant
  // (counters.real.binding.yaml) leaves the mailer's claim out, as
  // docs/grist-instrumentation.md §5's decision 4 has it.
  // grist: eventually default retiring real
  val everyPaymentAnswerServed =
    bankPayCalls.indices().forall(i => bankPayCalls[i].result != "unknown" implies
      bankPayRecords.indices().exists(j => bankPayRecords[j] == bankPayCalls[i]))
  // grist: eventually default retiring
  val everyMailAnswerServed =
    mailerSendCalls.indices().forall(i => mailerSendCalls[i].result != "unknown" implies
      mailerSendRecords.indices().exists(j => mailerSendRecords[j] == mailerSendCalls[i]))

  // test-only: every retirement paid out exactly once, by the bank's own
  // record of its payments (its served records, each naming its trigger).
  // At most once allows none, by right (a crash between the checkpoint
  // and the payment); an application that must pay needs another
  // guarantee.
  // grist: eventually exactly
  val everyRetirementPaidOnce =
    log.indices().forall(i => log[i].typ == "counter-retired" implies
      bankPayRecords.select(p => p.trigger == i + 1 and p.result == "ok").length() == 1)

  // test-only: every payment step's outcome is in the log (paid-out after
  // an ok one, grist.failed or grist.unknown after a failed one): a crash
  // after the payment loses it, by right; a runtime or an app that never
  // appends it conforms to the actions (the outcome is optional)
  // grist: eventually exactly
  val everyPayoutOutcomeLogged =
    atMostOnceOutcomesLogged(log, effects, "payout", t => if (t > 0 and t <= log.length()) payoutSucceeded(log, t) else [])

  // test-only: no payout's outcome is unknown (grist.unknown). At most
  // once admits one (a lost answer, a timeout: the bank may have paid);
  // a node that lets its effects in flight finish before it stops
  // (grist's drain) records none under graceful terminations alone,
  // where a node whose work is cancelled at the signal does
  // (stopNodeAtSignal)
  // grist: violable terminating
  val noPayoutUnknown = log.indices().forall(i => not(log[i].typ == "grist.unknown" and log[i].strs[0] == "payout"))

  // test-only: one ok welcome per counter. At least once allows more, by
  // right; an application whose effect must not repeat states it so
  // grist: violable once
  val welcomedOnce = log.indices().forall(i => effectsOf(effects, "welcome", i + 1, true) <= 1)
}

// The clients' side (counters.calls.binding.yaml), linearized.
module countersCalls {
  import grist.* from "./grist"
  import countersEvents.* from "./counters_gen"
  import countersRules.* from "./counters_rules"
  import countersCallsGen.* from "./counters_steps"
  export countersCallsGen.*

  // grist: invariant
  val uniqueNames = namesUnique(log)
}
