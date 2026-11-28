// A model with constants, proved three ways: as it is (for every MAX and
// ACCOUNTS), by its instance (for those constants alone), and by a
// module that fixes ACCOUNTS and leaves its own LIMIT open as MAX.
module counter {
  const MAX: int
  const ACCOUNTS: Set[str]
  assume maxNat = MAX >= 0
  var n: int

  action init = all { n' = 0 }
  action bump = all { n < MAX, n' = n + 1 }
  action step = bump

  val bounded = n >= 0 and n <= MAX
}

// the binding's constants
module counterInstance {
  import counter(MAX = 3, ACCOUNTS = Set("a", "b")).*
}

// ACCOUNTS fixed, MAX open as LIMIT
module counterLimit {
  const LIMIT: int
  assume limitNat = LIMIT >= 0
  import counter(MAX = LIMIT, ACCOUNTS = Set("a", "b")).*
}
