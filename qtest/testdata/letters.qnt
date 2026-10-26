// Letters a driver types, one action each (qtest/shrinkactions_test.go):
// a and c together violate noBug, whatever comes between; d after b is
// a step the model cannot take. ShrinkActions shrinks a long sequence to
// the letters each failure needs.
module letters {
  var seen: Set[str]

  action init = seen' = Set()

  action press(letter: str): bool = all {
    not(letter == "d" and seen.contains("b")),
    seen' = seen.union(Set(letter)),
  }

  action step = nondet l = Set("a", "b", "c", "d", "e", "f").oneOf() press(l)

  val noBug: bool = not(seen.contains("a") and seen.contains("c"))
}
