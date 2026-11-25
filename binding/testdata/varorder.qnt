// Six variables declared z..u: quint 0.33.1 typechecks them as
// [v u z y x w] (a toposort layer sorted by id as a string), and a module
// written before this one shifts the ids and that order (TestVarsSorted).
module order {
  var z: int
  var y: int
  var x: int
  var w: int
  var v: int
  var u: int

  action init = all { z' = 0, y' = 0, x' = 0, w' = 0, v' = 0, u' = 0 }
  action restore(a: int, b: int, c: int, d: int, e: int, f: int): bool =
    all { z' = a, y' = b, x' = c, w' = d, v' = e, u' = f }
  action setZ(a: int): bool = all { z' = a, y' = y, x' = x, w' = w, v' = v, u' = u }
  // assign z alone: lint lists the variables left, and names the first
  action onlyZ(a: int): bool = z' = a
  action resumeZ = z' = 0
  action step = nondet a = 0.to(3).oneOf() setZ(a)
}
