// "constructor" substrings are valid variable names. The Rust IPC parser
// must preserve them while continuing to reject the exact unsafe property.
module constructor_names {
  var constructorCount: int
  action init = constructorCount' = 0
  action step = all { constructorCount < 2, constructorCount' = constructorCount + 1 }
}
