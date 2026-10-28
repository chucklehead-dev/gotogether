// Folds over sets: those the evaluator gives (an operator known to
// commute, or a small set every order of which agrees), compared with
// quint's values on both backends, and those it refuses.
module folds {
  val sumBig = 1.to(20).fold(0, (s, x) => s + x * x)
  val sumCond = 1.to(20).fold(0, (s, x) => if (x % 2 == 0) s + x else s - x)
  val minBig = 1.to(20).fold(100, (m, x) => if (x * 3 < m) x * 3 else m)
  val maxBig = 1.to(20).fold(0, (m, x) => if (m >= x % 7) m else x % 7)
  val unionBig = 1.to(20).fold(Set(), (a, x) => a.union(Set(x % 5)))
  val putSmall = Set(1, 33, 2).fold(Map(), (m, x) => m.put(x, x * 2))
  val countSmall = Set(1, 33, 2).fold((0, 0), (a, x) => (a._1 + 1, a._2 + x))

  // a round-robin assignment: agrees folded sorted and reversed, not in
  // quint's order (review 11, F1)
  val roundRobin = Set(1, 33, 2).fold((0, Map()), (acc, n) => (acc._1 + 1, acc._2.put(n, acc._1 % 2)))._2
  // the second element folded
  val second = Set(1, 33, 2).fold((0, 0), (a, v) => if (a._1 == 1) (2, v) else (a._1 + 1, a._2))._2
  // order-independent, but past the sets checked over every order
  val putBig = 1.to(11).fold(Map(), (m, x) => m.put(x, x))
  // a min whose branch is not its comparison's term
  val notMin = 1.to(20).fold(100, (m, x) => if (x < m) x + 1 else m)
  // fails (a division by zero) in the order 2, 1 alone
  val failsSome = Set(1, 2).fold(0, (a, x) => 10 / (a + x - 2) + a)

  var out: (int, int, int, int, Set[int], int -> int, (int, int))
  action init = out' = (sumBig, sumCond, minBig, maxBig, unionBig, putSmall, countSmall)
  action step = out' = out
}
