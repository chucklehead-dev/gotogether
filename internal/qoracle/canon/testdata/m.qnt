module lib {
  const N: int
  type Pair[a] = (a, a)
  type V = A(int) | B
  pure def f(n: int): int = { val m = n + N; m }
  pure def g(n: int): int = { val m = f(n); val k = Set(m).fold(0, (a, n) => a + n); k }
  pure def sh(n: int): int = { val n2 = n; val r = { val n2 = n + 1; n2 }; r }
  pure def swap(p: Pair[int]): Pair[int] = (p._2, p._1)
  pure def id(z) = z
  pure def getf(r) = r.f
  pure def upd(r: { f: int, g: str }): { f: int, g: str } = { ...r, f: 1 }
  pure def mv(v: V): int = match v { | A(i) => i | B => 0 }
}
module m {
  import lib(N = 3) as L
  import lib(N = 4).*
  var x: int
  var log: List[int]
  action init = all { x' = L::g(0), log' = [] }
  action step = {
    nondet k = 1.to(3).oneOf()
    all {
      x' = f(x) + g(k) + id(sh(x)) + getf({ f: mv(A(k)) }),
      log' = log.append(swap((x, k))._1),
    }
  }
  val ok = x >= 0 and log.length() >= 0
}
