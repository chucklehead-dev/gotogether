// Types for TypeOf, Params and Nondets: a record, a sum with and without
// payloads, a tuple, an Option, a list, a map, a polymorphic def, an open
// record, and a nondet chosen inside an action step applies.
module types {
  type Acct = { name: str, limit: int }
  type Cmd = Open(str) | Close | Move({ a: int, b: int })
  type Pair = (int, str)
  type Option[a] = Some(a) | None
  var s: Set[int]
  var l: List[str]
  var r: Acct
  var o: Option[int]
  var c: Cmd
  var p: Pair
  var g: int -> Set[str]
  action init = all { s' = Set(), l' = [], r' = { name: "x", limit: 1 }, o' = None, c' = Close, p' = (1, "a"), g' = Map() }
  action act(x: Acct, y: Cmd, z: Option[int], w: Pair, v: List[str]): bool = all { s' = s, l' = v, r' = x, o' = z, c' = y, p' = w, g' = g }
  action grow = {
    nondet n = 1.to(3).oneOf()
    all { s' = s.union(Set(n)), l' = l, r' = r, o' = o, c' = c, p' = p, g' = g }
  }
  def idOf(a: Acct): Pair = (a.limit, a.name)
  def poly(e) = e
  def limitOf(a) = a.limit
  action step = {
    nondet x = Set({ name: "x", limit: 1 }).oneOf()
    nondet y = Set(Close, Open("a")).oneOf()
    nondet z = Set(None, Some(3)).oneOf()
    nondet w = Set((1, "a")).oneOf()
    nondet v = Set(["a"]).oneOf()
    any { act(x, y, z, w, v), grow }
  }
  val limits = limitOf(r)
}
