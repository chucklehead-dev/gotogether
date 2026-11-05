module cancellation {
  var x: int
  action init = x' = 0
  action step = x' = x + 1
  action expand = x' = 1.to(60000).size()
  val unused = {
    val ignored = 1.to(60000).size()
    7
  }
}
