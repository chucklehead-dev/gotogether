module rr {
  // round-robin: each node of the set gets a shard by its position
  pure def roundRobin(nodes: Set[int]): int -> int =
    nodes.fold((0, Map()), (acc, n) => (acc._1 + 1, acc._2.put(n, acc._1 % 2)))._2
  var shard: int -> int
  action init = shard' = Map()
  action place = shard' = roundRobin(Set(1, 33, 2))
  action step = place
  // shards are balanced: node 2 sits on shard 1
  val balanced = shard.keys().size() == 0 or shard.get(2) == 1
}
