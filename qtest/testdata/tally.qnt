// A total that each add raises by one, observed by its caller
// (linearize_test.go): a duplicated request a handler applies twice
// shows as a total no order explains.
module tally {
  var total: int
  action init = total' = 0
  action add: bool = total' = total + 1
  action step = add
}
