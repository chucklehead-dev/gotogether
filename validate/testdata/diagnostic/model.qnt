module obligations {
  var pending: Set[str]
  action init = pending' = Set()
  action request(id: str): bool = all {not(pending.contains(id)),pending' = pending.union(Set(id))}
  action answer(id: str): bool = all {pending.contains(id),pending' = pending.exclude(Set(id))}
  action step = {
    nondet id = Set("c1", "c2").oneOf()
    any {request(id),answer(id)}
  }
  val outstanding = pending.map(id => {kind: "cancellation", subject: "shared-order", intent: id, expected: "modeled inventory cancellation answer"})
  /// gotogether: diagnostic outstanding
  val complete = outstanding.size() == 0
}
