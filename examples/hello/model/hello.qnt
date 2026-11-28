// A counter service that applies each request exactly once. The network
// may deliver a request twice (a retry, a duplicate), so the service sees
// some requests again: it must recognise them (replay) and not count them.
module hello {
  // the requests the clients send, by id
  const REQUESTS: Set[int]

  var applied: Set[int]
  var count: int
  // ghosts, which no guard reads: the position at which each applied
  // request was counted, and the request counted at each position. They
  // state exactlyOnce without a set's size, so that Veil proves it for
  // every REQUESTS (DESIGN §116)
  var slot: int -> int
  var bySlot: int -> int

  action init = all { applied' = Set(), count' = 0, slot' = Map(), bySlot' = Map() }

  // a request seen for the first time is counted
  action apply(id: int): bool = all {
    REQUESTS.contains(id),
    not(applied.contains(id)),
    applied' = applied.union(Set(id)),
    count' = count + 1,
    slot' = slot.put(id, count + 1),
    bySlot' = bySlot.put(count + 1, id),
  }

  // a request seen again changes nothing
  action replay(id: int): bool = all {
    applied.contains(id),
    applied' = applied,
    count' = count,
    slot' = slot, bySlot' = bySlot,
  }

  // the client is done: every request it sends was applied
  action done = all {
    applied == REQUESTS,
    applied' = applied,
    count' = count,
    slot' = slot, bySlot' = bySlot,
  }

  action step = { nondet id = REQUESTS.oneOf() any { apply(id), replay(id), done } }

  // every applied request counted once: count == applied.size(), stated
  // pointwise. slot numbers the applied requests 1..count, one each, and
  // bySlot is its inverse: a bijection between applied and 1.to(count)
  val exactlyOnce = and {
    count >= 0,
    slot.keys() == applied,
    applied.forall(i => 1 <= slot.get(i) and slot.get(i) <= count and bySlot.keys().contains(slot.get(i)) and bySlot.get(slot.get(i)) == i),
    bySlot.keys().forall(k => 1 <= k and k <= count and applied.contains(bySlot.get(k)) and slot.get(bySlot.get(k)) == k),
    1.to(count).forall(k => bySlot.keys().contains(k)),
  }
}
