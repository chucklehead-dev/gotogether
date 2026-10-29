// The bank app's rules: every hole grist's generated skeleton (model/bank_steps.qnt,
// grist model) calls, and the read models' folds. Regenerating the
// skeleton never touches this file.

module bankRules {
  import grist.* from "./grist"
  import bankEvents.* from "./bank_gen"

  // the strings the model on its own (step: gotogether simulate) draws
  // arguments from: few, so that they collide
  pure val STRS: Set[str] = Set("a", "b", "z")

  // the accounts the bank holds (bank.Accounts)
  pure val ACCOUNTS: Set[str] = Set("a", "b")

  // the integers it draws arguments from
  pure val INTS: Set[int] = 1.to(2)

  // pay's rule: the outcome its decision reaches on log ("ok" or
  // "unknown-account")
  pure def payOutcome(log: List[Event], c: PayArgs): str = if (ACCOUNTS.contains(c.account)) "ok" else "unknown-account"

  // the events pay appends when ok (their causes are stamped by its action)
  pure def payEvents(log: List[Event], c: PayArgs): List[Event] = [paid(c.account, c.amount, c.payout)]

  // the reads pay's rule needs for result: none (Unconditioned: it reads
  // nothing)
  pure def payNeeds(c: PayArgs, result: str): Set[Read] = Set()
}
