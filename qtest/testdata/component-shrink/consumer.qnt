module consumer {
  var deliveries: int
  action init = deliveries' = 0
  action apply = deliveries' = deliveries + 1
  action observe = deliveries' = deliveries
  action step = any { apply, observe }
  val exactlyOnce = deliveries <= 1
  val neverDelivered = deliveries == 0
}
