// A model whose actions take nested types, for toQuint: records, sums
// with and without payloads, a parametric alias, sets of tuples, maps of
// lists, records in records (grist's read set and event batches).
module typed {
  type Option[a] = Some(a) | None
  type Read = { typ: str, tags: Set[(str, str)] }
  type Event = Created({ counter: str, name: str }) | Renamed({ counter: str, name: str }) | Reset
  type Batch = { events: List[Event], at: int, cause: Option[str] }

  var log: List[Event]
  var reads: Set[Read]
  var counts: str -> List[int]
  var last: Option[Batch]

  action init = all { log' = [], reads' = Set(), counts' = Map(), last' = None }

  // every parameter defaults to its same-named field, by its type
  action commit(batch: Batch, rs: Set[Read], tally: str -> List[int], pair: (int, bool), nested: { a: { b: List[(int, str)] } }, n: int): bool = all {
    log' = log.concat(batch.events),
    reads' = reads.union(rs),
    counts' = tally,
    last' = Some(batch),
  }

  action reset(why: Option[str]): bool = all {
    log' = log.append(Reset), reads' = reads, counts' = counts, last' = last,
  }

  action step = any {
    commit({ events: [Created({ counter: "c", name: "n" })], at: 1, cause: None }, Set(), Map(), (1, true), { a: { b: [] } }, 0),
    reset(None),
  }
}
