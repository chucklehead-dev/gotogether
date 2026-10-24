// A counter of processes that crash: each inc adds an interned id to the
// ids seen, a restart counts a crash, a look observes both counts. Its
// binding interns the ids, infers restarts and sizes IDS from the
// history: what a search's candidates must be planned within.
module procs {
  const IDS: Set[int]
  var count: int
  var crashes: int
  var seen: Set[int]

  action init = all { count' = 0, crashes' = 0, seen' = Set() }
  action inc(id: int): bool = all { IDS.contains(id), count' = count + 1, crashes' = crashes, seen' = seen.union(Set(id)) }
  action restart: bool = all { count' = count, crashes' = crashes + 1, seen' = seen }
  action look: bool = all { count' = count, crashes' = crashes, seen' = seen }
  action step = any { nondet i = IDS.oneOf() inc(i), restart, look }
}
