module slots {
  var admitted: int
  action init = admitted' = 0
  action admit(ok: bool): bool = all {
    ok == (admitted == 0),
    admitted' = if (ok) admitted + 1 else admitted,
  }
  val atMostOne = admitted <= 1
}
