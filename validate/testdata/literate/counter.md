# A counter, written as its design document

This document is the model: gotogether reads its `quint` blocks, in order,
into `counter.qnt` beside it (literate_test.go). The counter starts at
zero and grows by one up to `MAX`.

```quint
module counter {
  pure val MAX = 3
  var n: int

  action init = n' = 0
  action inc = all { n < MAX, n' = n + 1 }
  action step = inc
```

What it promises, and one promise it cannot keep:

```quint
  // never negative: holds
  val nonNegative = n >= 0
  // stays below MAX: the design reaches MAX, so this one breaks
  val belowMax = n < MAX
}
```
