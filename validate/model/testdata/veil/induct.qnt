// A model whose property xNonNegative holds but is not inductive on its
// own: x takes y's old value, so a state with x >= 0 and y < 0 leaves it
// after shift. The lemma yNonNegative makes it inductive. settled is a
// settle flag step never sets: settledCorrectly holds vacuously.
module induct {
  var x: int
  var y: int
  var settled: bool

  action init = all { x' = 0, y' = 0, settled' = false }
  action shift = all { y < 3, x' = y, y' = y + 1, settled' = settled }
  action step = shift

  val xNonNegative = x >= 0
  val yNonNegative = y >= 0
  val xSmall = x < 10
  val xBounded = x > 0 implies x < 3
  // false at init: induction's counterexample is init's state
  val xPositive = x > 0
  val settledCorrectly = settled implies x > 100
  val neverSettled = not(settled)
}
