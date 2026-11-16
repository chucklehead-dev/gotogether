// Types for TypeOf, Params and Nondets: a record, a sum with and without
// payloads, a tuple, an Option, a list, a map, a polymorphic def, an open
// record, and a nondet chosen inside an action step applies. And the
// shapes ActionPicks attributes a step's nondets through: a call's
// arguments (through an expression and a let), a guard, a nondet two
// calls share, one bound in the action's body, a helper action, and a
// match, which it does not follow.
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
  // a helper the actions below apply: its parameters are what flows in
  action keepWith(ns: Set[int], ls: List[str]): bool = all { s' = ns, l' = ls, r' = r, o' = o, c' = c, p' = p, g' = g }
  action attempt(d: int): bool = keepWith(s.union(Set(d)), l)
  action failed(outcome: str): bool = keepWith(s, l.append(outcome))
  action opened(t: str): bool = keepWith(s, l.append(t))
  action closed: bool = keepWith(s, l)
  def idOf(a: Acct): Pair = (a.limit, a.name)
  def poly(e) = e
  def limitOf(a) = a.limit
  action step = {
    nondet x = Set({ name: "x", limit: 1 }).oneOf()
    nondet y = Set(Close, Open("a")).oneOf()
    nondet z = Set(None, Some(3)).oneOf()
    nondet w = Set((1, "a")).oneOf()
    nondet v = Set(["a"]).oneOf()
    nondet d = 1.to(3).oneOf()
    nondet k = 1.to(2).oneOf()
    nondet outcome = Set("ok", "late").oneOf()
    nondet gate = Set(true, false).oneOf()
    nondet cmd = Set(Close, Open("b")).oneOf()
    any {
      act(x, y, z, w, v),
      grow,
      { val e = d + k
        attempt(e) },
      all { gate, failed(outcome), k > 0 },
      match cmd { | Open(t) => opened(t) | _ => closed },
    }
  }
  val limits = limitOf(r)
}
