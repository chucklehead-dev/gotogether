// Publication, the other model of the shared tree.
module publication {
  var served: int
  var expired: bool

  // A page is never served expired: its
  // lease is checked first. Then served.
  val noExpiredServe = not(expired) or served == 0

  // a mutant's witness: nothing enforces it, by design
  val witness = served > 2

  action init = all { served' = 0, expired' = false }
  action serve = all { not(expired), served' = served + 1, expired' = expired }
  action step = serve
}
