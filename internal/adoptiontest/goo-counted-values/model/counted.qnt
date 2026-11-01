// Independent bounded arithmetic contract from Goo's counted-values docs.
// Individual counts fit uint64; their mathematical sum need not fit uint64.
// This model does not cover SQL, grouping identity, clocks or source snapshots.
module counted {
  pure val maxCount = 18446744073709551615
  pure def groupSum(xs: List[int]): int = xs.foldl(0, (acc, n) => acc + n)
  // Group ranking/order is not part of this contract. Compare occurrence counts,
  // not sets, so duplicate counts belonging to distinct groups are preserved.
  pure def occurrences(xs: List[int], n: int): int =
    xs.foldl(0, (acc, x) => if (x == n) acc + 1 else acc)
  pure def sameCounts(xs: List[int], ys: List[int]): bool =
    xs.length() == ys.length() and
    xs.indices().forall(i => occurrences(xs, xs[i]) == occurrences(ys, xs[i]))
  pure def classify(total: int, xs: List[int]): str =
    if (groupSum(xs) != total) "changed_between_reads"
    else if (total == 0) "no_data"
    else "observed"

  var state: str
  var count: int
  var lastGroups: List[int]

  action init = all {
    state' = "no_data",
    count' = 0,
    lastGroups' = List(),
  }

  action lookup(total: int, groups: List[int]): bool = all {
    total >= 0 and total <= maxCount,
    groups.length() <= 2,
    groups.foldl(true, (valid, n) => valid and n > 0 and n <= maxCount),
    state' = classify(total, groups),
    count' = total,
    lastGroups' = groups,
  }

  pure val requests = Set(
    { total: 0, groups: List() },
    { total: 9007199254740993, groups: [9007199254740993] },
    { total: 18446744073709551615, groups: [18446744073709551615] },
    { total: 0, groups: [18446744073709551615, 1] }
  )
  action step = {
    nondet request = requests.oneOf()
    lookup(request.total, request.groups)
  }

  val countDomain = count >= 0 and count <= maxCount
  val positiveGroups = lastGroups.foldl(true, (valid, n) => valid and n > 0 and n <= maxCount)
  val stateClassification = state == classify(count, lastGroups)

  run countMultisetTest = assert(
    sameCounts([1, 2], [2, 1]) and
    sameCounts([1, 1], [1, 1]) and
    sameCounts(List(), List()) and
    not(sameCounts([1, 1], [1, 2])) and
    not(sameCounts([1], [1, 1]))
  )
  run emptyTest = init.then(lookup(0, List()))
    .expect(state == "no_data" and count == 0 and lastGroups == List())
  run aboveExactDoubleTest = init.then(lookup(9007199254740993, [9007199254740993]))
    .expect(state == "observed" and count == 9007199254740993 and groupSum(lastGroups) == count)
  run maximumCountTest = init.then(lookup(18446744073709551615, [18446744073709551615]))
    .expect(state == "observed" and count == maxCount and groupSum(lastGroups) == count)
  run mathematicalOverflowTest = init.then(lookup(0, [18446744073709551615, 1]))
    .expect(state == "changed_between_reads" and count == 0 and groupSum(lastGroups) == 18446744073709551616)
}
