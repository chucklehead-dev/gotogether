// The fulfillment app's rules: every hole grist's generated skeleton (model/fulfillment_steps.qnt,
// grist model) calls, and the read models' folds. Regenerating the
// skeleton never touches this file.

module fulfillmentRules {
  import grist.* from "./grist"
  import fulfillmentEvents.* from "./fulfillment_gen"

  // the strings the model on its own (step: gotogether simulate) draws
  // arguments from: few, so that they collide
  pure val STRS: Set[str] = Set("a", "b", "c", "d")

  // the integers it draws arguments from
  pure val INTS: Set[int] = 1.to(2)

  // start's rule: the outcome its decision reaches on log ("ok")
  pure def startOutcome(log: List[Event], c: StartArgs): str = "ok"

  // the events start appends when ok (their causes are stamped by its action)
  pure def startEvents(log: List[Event], c: StartArgs): List[Event] = [started(c.order)]

  // the reads start's rule needs for result: its committed step's read set
  // must cover each (covers)
  pure def startNeeds(c: StartArgs, result: str): Set[Read] = Set()
}
