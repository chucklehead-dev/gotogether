// Bounded Read contract: 16 authored YAML inputs, two accepted shapes.
// Input IDs denote fixed fixtures, never observations or implementation branches.
module reader {
  const CASES: Set[int]
  var lastCase: int
  var accepted: bool
  var fieldCount: int
  var measureCount: int
  action init = all {
    lastCase' = -1, accepted' = false,
    fieldCount' = 0, measureCount' = 0,
  }
  action read(caseID: int): bool = all {
    CASES.contains(caseID),
    lastCase' = caseID,
    accepted' = (caseID == 0 or caseID == 1),
    fieldCount' = if (caseID == 0 or caseID == 1) 3 else 0,
    measureCount' = if (caseID == 0) 1 else if (caseID == 1) 3 else 0,
  }
  action step = {
    nondet caseID = CASES.oneOf()
    read(caseID)
  }
  // Structural safety, selected explicitly. Reachability helpers below are not safety.
  val boundedCase = lastCase == -1 or CASES.contains(lastCase)
  val acceptedShape = accepted implies (fieldCount == 3 and (measureCount == 1 or measureCount == 3))
  val reachedAccepted = accepted
  val reachedRejected = lastCase >= 2
}
