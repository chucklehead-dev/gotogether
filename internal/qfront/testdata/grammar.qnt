/// Every alternative of Quint.g4 (v0.33.1) the parser has, once at least:
/// internal/qfront's differential test compares its parse with quint's.
module base {
  const N: int
  type Id = str
  pure def double(x: int): int = 2 * x
}

/// the main module
module grammar {
  import base(N = 3) as B
  import base(N = 4).* 
  import base.double
  export B.double
  /// a constant
  const S: Set[int]
  var x: int
  var log: List[str]
  var m: int -> Set[str]
  var r: { a: int, b: (bool, str), c: () }
  var t: (int, str,)
  type Abstract
  type Alias = int
  type Pair[a, b] = (a, b)
  type Record[a] = { f: a, g: List[a] }
  type Open[r] = { f: int | r }
  type Op = (int, str) => bool
  type Op1 = int => int
  type Fun = int -> str -> bool
  type Cmp = (int -> str) => Set[Pair[int, str]]
  /// the sum's doc goes to its last constructor
  type Shape = | Circle(int) | Square({ side: int }) | Dot
  type Option[a] = Some(a) | None
  type Only = | Only1
  type Lone = Only
  type Unit = | Nothing
  assume _ = N > 0
  assume positive = S.forall(s => s > 0)

  val v1 = 1 + 2 * 3 - 4 / 5 % 6
  val v2 = 2 ^ 3 ^ 2
  val v3 = -x ^ 2 + -x * 3
  val v4 = x > 1 and x < 9 or x >= 2 and x <= 8 implies x != 3 iff x == 4
  val v5 = (x > 1) leadsTo (x > 2)
  val v6 = if (x > 0) "pos" else if (x < 0) "neg" else "zero"
  val v7 = and { x > 0, x < 10, }
  val v8 = or { x > 0, x < 10 }
  val v9 = (1, "a", true)
  val v10 = ()
  val v11 = 1 -> 2
  val v12 = { a: 1, b: (true, "s"), c: () }
  val v13 = { ...v12, a: 2, }
  val v14 = [1, 2, 3,]
  val v15 = []
  val v16 = v9._1 + v12.a + log[0].length()
  val v17 = S.map(s => s + 1).filter(s => s > 2).fold(0, (acc, s) => acc + s)
  val v18 = S.map(((a, b)) => a).size()
  val v19 = Set(1, 2).union(Set(3)).contains(1)
  val v20 = List(1, 2).concat([3]).select(_ => true)
  val v21 = Map(1 -> 2).get(1) + m.keys().size()
  val v22 = { val y = 1 val z = y + 1; z * 2 }
  val v23 = { pure def f(a) = a + 1 f(2) }
  val v24 = match Circle(1) { | Circle(r) => r | Square(_) => 0 | _ => 1 }
  val v25 = match None { Some(a) => a | None => 0 }
  val v26 = q::debug("x", x) + q::debug(x)
  val v27 = 0x1F + 1_000_000 + 0
  val v28 = "a string, with // and /* */"
  val v29 = B::double(1) + double(2)
  val v30 = v4.and(true).or(false).implies(true).iff(true)
  val v31 = and(true, false)
  val (d1, _, d3) = (1, 2, 3)
  pure val { a, c } = v12
  val v32 = { val (p, q) = (1, 2) p + q }
  val v33 = { pure val { a } = v12 a }

  def deprecated(p, q) = p + q
  def header(p: int): int
  val headerVal: int
  def withRet: int = 1
  def unit() = 1
  def trailing(p: int, q: str,): int = p;
  pure def poly(p: a): a = p
  temporal always_ok = always(x >= 0)
  run test = init.then(step).then(all { x' = x, log' = log, m' = m, r' = r, t' = t }).expect(x >= 0)

  action init = all {
    x' = 0,
    log' = [],
    m' = Map(),
    r' = { a: 0, b: (true, ""), c: () },
    t' = (0, ""),
  }
  action step = any {
    nondet s = S.oneOf()
    x' = x + s,
    all { x' = x, log' = log.append("a") },
  }
}
