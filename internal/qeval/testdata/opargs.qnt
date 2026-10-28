// An action called with operators for arguments, as Choreo's step_with
// is: each call its own successors, though no key writes an operator
// (eval_test.go).
module opargs {
  var x: int

  action init = x' = 1
  action apply(f: int => int): bool = x' = f(x)
  action step = any { apply(n => n + 1), apply(n => n * 3) }
}
