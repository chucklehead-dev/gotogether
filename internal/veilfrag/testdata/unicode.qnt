// A settle flag named by an action's prefix (settle, settled), after text
// that is not ASCII (§116, ≥, 😀): quint's source map counts characters,
// not bytes, so an antecedent's text is cut by characters.
module unicode {
  var settled: bool
  var unsettled: bool
  var x: int

  // § the settle the binding records; ≥ 0 always; 😀
  action init = all { x' = 0, settled' = false, unsettled' = true }
  action settle = all { x' = x, settled' = true, unsettled' = false }
  action step = all { x' = x + 1, settled' = settled, unsettled' = unsettled }

  val settledCorrectly = settled implies x >= 0
  val neverSettled = not(settled)
}
