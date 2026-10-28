// A choice some of whose values disable the action: quint's replay picks
// a value at random (`quint test` fails pickSeven at most seeds), the
// evaluator takes the step with the value that enables it.
module choice {
  var v: int
  action init = v' = 0
  action pickSeven = { nondet x = oneOf(1.to(1000)); all { x == 7, v' = x } }
  action pickAny = { nondet x = oneOf(1.to(10)); v' = 3 }
  action step = any { pickSeven, pickAny }
}
