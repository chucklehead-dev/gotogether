// An assume in an imported module, which Veil leaves out of the main
// module's proof, and one restated in main.
module base {
  const N: int
  assume nNat = N >= 0
  var x: int
  action init = x' = N
  action step = x' = x + 1
  val nonNeg = x >= 0
}

module assumes {
  import base.*
}

module restated {
  import base.*
  assume nNatHere = N >= 0
}
