- `connect.Binding.Observes(name, picks...)` compares a bound method's
  results, choices the implementation makes, with the model's picks of
  them, one pick per result: a mismatch reads "the implementation chose
  account = alice; the model's choice is bob". It is `Returns`' inverse
  (a result against a def the model computes), and an action takes one
  of the two; an observed result counts as checked.
