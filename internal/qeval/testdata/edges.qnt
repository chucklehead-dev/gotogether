// Builtins at their edges, each a val: the test compares the evaluator's
// value of each with quint's (init stores them all in one tuple).
module edges {
  def outer(q: int): Set[int] = { def inner(z) = z + q; Set(1, 2).map(inner) }
  def twice(f: int => int, x: int): int = f(f(x))
  type V = A(int) | B | C(str)
  pure def tag(v: V): str = match v { A(n) => "a" | C(s) => s | _ => "other" }

  val mod1 = (-7) % 2
  val mod2 = 7 % (-2)
  val div1 = (-7) / 2
  val pow1 = 2^10
  val clos = outer(10)
  val lam = twice(x => x * 3, 2)
  val mapDup = Map(1 -> "a", 1 -> "b").get(1)
  val recEq = { a: 1, b: "x" } == { b: "x", a: 1 }
  val recSet = Set({ a: 1, b: 2 }, { b: 2, a: 1 }).size()
  val foldl1 = [1, 2, 3].foldl("", (acc, x) => acc)
  val foldr1 = [1, 2, 3].foldl(List(), (acc, x) => acc.append(x))
  val sl = [10, 20, 30, 40].slice(1, 3)
  val rg = range(2, 5)
  val ix = [5, 6, 7].indices()
  val tp = tuples(Set(1, 2), Set("a", "b")).size()
  val ps = Set(1, 2, 3).powerset().size()
  val sum = Set(4, 5, 6).fold(0, (s, x) => s + x)
  val m1 = tag(A(3))
  val m2 = tag(B)
  val m3 = tag(C("c"))
  val nat = Nat.contains(-1) or Int.contains(-1)
  val setBy1 = Map("k" -> 1).setBy("k", x => x + 1).get("k")
  val keys1 = Map(3 -> 1, 1 -> 2).keys()
  val flat = flatten(Set(Set(1), Set(2, 1)))
  val nth1 = [1, 2, 3][2]
  val ex = Set(1, 2, 3).exists(x => x > 2) and Set(1, 2, 3).forall(x => x > 0)
  val sub = Set(1).subseteq(Set(1, 2)) and not(Set(3).subseteq(Set(1, 2)))
  val imp = false implies (1 / 0 == 0)
  val foldOrder = Set(10, 5, 1, 7).fold(List(), (l, x) => l.append(x))
  // quint's backends differ (Rust: [], TypeScript: [5, 4, 3]): refused
  val rangeDown = range(5, 2)

  var out: (int, int, int, int, Set[int], int, str, bool, int, str, List[int], List[int], List[int], Set[int], int, int, int, str, str, str, bool, int, Set[int], Set[int], int, bool, bool, bool)
  action init = out' = (mod1, mod2, div1, pow1, clos, lam, mapDup, recEq, recSet, foldl1, foldr1, sl, rg, ix, tp, ps, sum, m1, m2, m3, nat, setBy1, keys1, flat, nth1, ex, sub, imp)
  action step = out' = out
}
