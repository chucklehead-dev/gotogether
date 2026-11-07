// Mathematical commutativity does not make bounded intermediate arithmetic
// order independent: one order can overflow while another returns a value.
module foldOverflow {
  val maxInt = 9223372036854775807
  val minInt = (-9223372036854775807) - 1

  val nightly = Set((-9223372036854775807), 10).fold(9223372036854775807, (a, x) => a + x)
  val subtract = Set((-9223372036854775807), 10).fold((-9223372036854775807), (a, x) => a - x)
  val unsafeLarge = Set((-9223372036854775807)).union(1.to(11)).fold(maxInt, (a, x) => a + x)
  val unsafeSubtractLarge = Set((-9223372036854775807)).union(1.to(11)).fold((-9223372036854775807), (a, x) => a - x)
  val unsafeConditional = Set((-9223372036854775807)).union(1.to(11)).fold(maxInt, (a, x) => if (x < 0) a + x else x + a)
  // Net zero per element, but the first subexpression still overflows.
  val unsafeNestedOrder = Set(-1, 1).fold(maxInt - 1, (a, x) => if (x < 0) (a + 1) - 1 else a + 1)
  val unsafeNestedOrderLarge = (-10).to(10).fold(maxInt - 1, (a, x) => if (x < 0) (a + 1) - 1 else if (x == 1) a + 1 else a)
  val unsafeNested = 1.to(20).fold(maxInt, (a, x) => (a + 1) - 1)

  val safeLarge = 1.to(20).fold(0, (a, x) => a + x)
  val safeMixed = (-20).to(20).fold(0, (a, x) => a + x)
  val safeConditional = 1.to(20).fold(0, (a, x) => if (x % 2 == 0) a + x else a - x)
  val safeSubtract = 1.to(20).fold(1000, (a, x) => a - x)
  val safeRightAcc = 1.to(20).fold((-1000), (a, x) => x + a)
  val safeNestedMax = 1.to(20).fold(maxInt - 20, (a, x) => (a + x) - x)
  val safeNestedMin = 1.to(20).fold(minInt + 20, (a, x) => (a - x) + x)
  val safeConditionalMax = 1.to(20).fold(maxInt - 200, (a, x) => if (x % 2 == 0) a + x else a - x)

  val safeBoundaryMax = 1.to(20).fold(maxInt - 210, (a, x) => a + x)
  val safeBoundaryMin = 1.to(20).fold(minInt + 210, (a, x) => a - x)
  // The net delta does not itself fit int64, although every actual operation
  // fits for every set order. The proof must not overflow its own arithmetic.
  val safeWideDelta = 1.to(20).fold(minInt, (a, x) => if (x == 1) (a + maxInt) + maxInt else a)
  val safeWideSubtract = 1.to(20).fold(maxInt, (a, x) => if (x == 1) (a - maxInt) - maxInt else a)

  // These errors are independent of the accumulator and occur in every order.
  val independentError = 1.to(20).fold(0, (a, x) => a + (1 / (x - x)))
  val independentBranchError = 1.to(20).fold(0, (a, x) => if (x == 13) a + (1 / 0) else a + x)

  pure def sumOf(initial: int, items: Set[int]): int = items.fold(initial, (a, x) => a + x)
  pure def subtractOf(initial: int, items: Set[int]): int = items.fold(initial, (a, x) => a - x)
  pure def addCancelOf(initial: int, items: Set[int]): int = items.fold(initial, (a, x) => (a + x) - x)
  pure def subtractCancelOf(initial: int, items: Set[int]): int = items.fold(initial, (a, x) => (a - x) + x)
  pure def conditionalOf(initial: int, items: Set[int]): int = items.fold(initial, (a, x) => if (x < 0) (a + 1) - 1 else a + x)
  pure def conditionalLowerOf(initial: int, items: Set[int]): int = items.fold(initial, (a, x) => if (x < 0) (a - 1) + 1 else a - x)

  var out: int
  action init = out' = 0
  action step = out' = out
}
