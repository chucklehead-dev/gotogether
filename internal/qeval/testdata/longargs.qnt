// Two transitions whose labels differ only past 200 characters (an
// argument's text, which Event.String cuts short): the graph's labels
// must keep them apart (graph_test.go).
module longargs {
  var v: List[int]

  action init = v' = []
  action store(l: List[int]): bool = v' = l
  action step = { nondet i = 0.to(1).oneOf() store(range(0, 100).append(i)) }
}
