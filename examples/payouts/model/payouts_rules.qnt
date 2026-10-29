// The payouts app's rules: every hole grist's generated skeleton (model/payouts_steps.qnt,
// grist model) calls, and the read models' folds. Regenerating the
// skeleton never touches this file.

module payoutsRules {
  import grist.* from "./grist"
  import payoutsEvents.* from "./payouts_gen"

  // the strings the model on its own (step: gotogether simulate) draws
  // arguments from: few, so that they collide
  pure val STRS: Set[str] = Set("a", "b", "z")

  // the integers it draws arguments from
  pure val INTS: Set[int] = 0.to(2)

  // request's rule: the outcome its decision reaches on log ("ok" or
  // "duplicate" or "invalid")
  pure def requestOutcome(log: List[Event], c: RequestArgs): str =
    if (c.amount <= 0) "invalid" else if (requestsEvents(log, c.payout).length() > 0) "duplicate" else "ok"

  // the events request appends when ok (their causes are stamped by its
  // action)
  pure def requestEvents(log: List[Event], c: RequestArgs): List[Event] = [requested(c.payout, c.account, c.amount)]

  // the reads request's rule needs for result: its committed step's read set
  // must cover each (covers)
  pure def requestNeeds(c: RequestArgs, result: str): Set[Read] = if (result == "invalid") Set() else requestsReads(c.payout)

  // the sent event send appends after its effect succeeded for the trigger at
  // position trigger (prefix: the log through it)
  pure def sendSuccess(prefix: List[Event], trigger: int): List[Event] = [sent(requestedPayout(prefix[trigger - 1]), trigger)]

  // whether send's handler skips the trigger at position trigger (returns
  // Skip: its checkpoint alone, no effect), reading prefix (the log through
  // it)
  pure def sendSkips(prefix: List[Event], trigger: int): bool = false

  // the request send's effect makes of bank/pay for the trigger at position
  // trigger, reading prefix (the log through it)
  pure def sendBankPay(prefix: List[Event], trigger: int): BankPayRequest = {
    account: requestedAccount(prefix[trigger - 1]),
    amount: requestedAmount(prefix[trigger - 1]),
    payout: requestedPayout(prefix[trigger - 1]),
  }
}
