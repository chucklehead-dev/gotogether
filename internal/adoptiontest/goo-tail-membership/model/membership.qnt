// Authored only from docs/TAIL_SELECTION_CUSTODY.md public contract.
// Two slots; integer seconds; no capacity, publisher, browser or concurrency claim.
module membership {
  type Member = { key: str, body: str, from: int, to: int, observed: int,
    basis: str, late: bool, limited: bool }
  type Snapshot = { issued: bool, observed: int, connection: str, selector: str,
    members: List[Member] }
  pure val emptySnapshot: Snapshot = { issued: false, observed: 0,
    connection: "", selector: "", members: List() }
  var clock: int
  var bank: List[Snapshot]
  var accepted: bool
  var expiry: int
  var expectedMembers: List[Member]
  var expectedConnection: str
  var expectedSelector: str
  var expiredRequest: bool

  pure def sameMembers(xs: List[Member], ys: List[Member]): bool =
    xs.length() == ys.length() and xs.indices().forall(i =>
      xs.foldl(0, (n, x) => if (x == xs[i]) n + 1 else n) == ys.foldl(0, (n, x) => if (x == xs[i]) n + 1 else n))

  action init = all {
    clock' = 0, bank' = [emptySnapshot, emptySnapshot], accepted' = false,
    expiry' = 0, expectedMembers' = List(), expectedConnection' = "",
    expectedSelector' = "", expiredRequest' = false,
  }
  action reset = init
  action advance(now: int): bool = all {
    now >= clock, clock' = now, bank' = bank, accepted' = accepted,
    expiry' = expiry, expectedMembers' = expectedMembers,
    expectedConnection' = expectedConnection, expectedSelector' = expectedSelector,
    expiredRequest' = expiredRequest,
  }
  // Basis is a native opaque identity, observed at issue rather than fabricated.
  action issue(slot: int, observed: int, connection: str, selector: str,
      members: List[Member], observedBasis: str): bool = all {
    Set(0, 1).contains(slot), observed <= clock,
    members.length() > 0 and members.length() <= 2,
    members.indices().forall(i => members[i].observed == observed and
      members[i].from < members[i].to),
    clock' = clock,
    accepted' = clock < observed + 120,
    expiry' = observed + 120,
    bank' = if (clock < observed + 120) bank.replaceAt(slot, {
      issued: true, observed: observed, connection: connection, selector: selector,
      members: members.foldl(List(), (acc, m) => acc.append({ ...m, basis: observedBasis })) }) else bank,
    expectedMembers' = List(), expectedConnection' = "", expectedSelector' = "",
    expiredRequest' = false,
  }
  def hasMember(s: int, key: str): bool =
    if (Set(0, 1).contains(s)) bank[s].issued and
      bank[s].members.indices().exists(i => bank[s].members[i].key == key) else false
  def liveOwner(s: int, connection: str, selector: str): bool =
    if (Set(0, 1).contains(s)) bank[s].issued and
      clock < bank[s].observed + 120 and bank[s].connection == connection and
      bank[s].selector == selector else false
  action resolve(slots: List[int], memberKeys: List[str], connection: str, selector: str): bool = {
    val shape = slots.length() == memberKeys.length() and slots.length() > 0 and slots.length() <= 4
    val valid = shape and memberKeys.indices().forall(i => memberKeys.indices().forall(j => i == j or memberKeys[i] != memberKeys[j])) and
      slots.indices().forall(i => hasMember(slots[i], memberKeys[i]) and
        liveOwner(slots[i], connection, selector))
    all {
      clock' = clock, bank' = bank, accepted' = valid, expiry' = expiry,
      expectedMembers' = if (valid) slots.foldl(List(), (acc, s) => {
        val wantedKey = memberKeys[acc.length()]
        bank[s].members.foldl(acc, (out, m) => if (m.key == wantedKey) out.append(m) else out)
      }) else List(),
      expectedConnection' = if (valid) connection else "",
      expectedSelector' = if (valid) selector else "",
      expiredRequest' = slots.indices().exists(i => if (Set(0, 1).contains(slots[i]))
        bank[slots[i]].issued and clock >= bank[slots[i]].observed + 120 else false),
    }
  }
  val bounded = bank.length() == 2 and clock >= 0
  val atomic = accepted or expectedMembers.length() == 0
  action step = advance(clock + 1)
  pure val rowA: Member = { key: "a", body: "body-a", from: -60, to: 0,
    observed: 0, basis: "", late: false, limited: false }
  pure val rowB: Member = { key: "b", body: "body-b", from: 0, to: 60,
    observed: 60, basis: "", late: true, limited: true }
  run expiryBoundaryTest = init
    .then(issue(0, 0, "c", "s", [rowA], "basis-a"))
    .then(advance(119)).then(resolve([0], ["a"], "c", "s"))
    .expect(accepted and expectedMembers.length() == 1)
    .then(advance(120)).then(resolve([0], ["a"], "c", "s"))
    .expect(not(accepted) and expiredRequest and expectedMembers == List())
  run delayedIssueTest = init.then(advance(120))
    .then(issue(0, 0, "c", "s", [rowA], "basis-a"))
    .expect(not(accepted) and not(bank[0].issued) and expiry == 120)
  run mixedExpiryTest = init.then(issue(0, 0, "c", "s", [rowA], "basis-a"))
    .then(advance(60)).then(issue(1, 60, "c", "s", [rowB], "basis-b"))
    .then(resolve([0, 1], ["a", "b"], "c", "s"))
    .expect(accepted and expectedMembers.length() == 2)
    .then(advance(120)).then(resolve([0, 1], ["a", "b"], "c", "s"))
    .expect(not(accepted) and expectedMembers == List())
  run ownerAndDuplicateTest = init.then(issue(0, 0, "c", "s", [rowA], "basis-a"))
    .then(resolve([0], ["a"], "other", "s")).expect(not(accepted))
    .then(resolve([0], ["a"], "c", "other")).expect(not(accepted))
    .then(resolve([0, 0], ["a", "a"], "c", "s")).expect(not(accepted))
    .then(resolve([0, -1], ["a", "forged"], "c", "s"))
    .expect(not(accepted) and expectedMembers == List())
  run firstOfTwoMembersTest = init
    .then(issue(0, 0, "c", "s", [rowA, { ...rowA, key: "second", body: "second-body" }], "basis-a"))
    .then(resolve([0], ["a"], "c", "s"))
    .expect(accepted and expectedMembers.length() == 1 and expectedMembers[0].key == "a")
}
