// Where an expression could go on or end, ANTLR reads on and ends it if
// going on fails later: a let's value then ends before what starts its
// body (quint's examples/classic/distributed/ClockSync/clockSync6.qnt,
// testFixture/_1080tupleDestructuring.qnt).
module ambiguity {
  pure def minus(p: int, q: int, b: int): bool = {
    val d = p - q
    -b <= d
  }
  pure def tuple(t1: (int, int), t2: (int, int)): (int, int) = {
    val (x1, y1) = t1
    val (x2, y2) = t2
    (x1 + x2, y1 + y2)
  }
  pure def list(l: List[int]): List[int] = {
    val m = l
    [1, 2]
  }
  pure def inLambda(s: Set[int], b: int): bool = s.forall(p => {
    val d = p - b
    -b <= d
  })
  pure def binary(a: int, b: int): int = a
    - b
}
