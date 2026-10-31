// A successful let and two uses of a deferred model error. The argument keeps
// division runtime-dependent, and reading the error is distinct from ignoring it.
module env {
  def successfulLet(denom: int): int = { val bound = denom + 1; bound }
  def unusedError(denom: int): int = { val bound = 1 / denom; 7 }
  def referencedError(denom: int): int = { val bound = 1 / denom; bound }

  var slot: int
  action init = slot' = 0
  action step = slot' = slot
}
