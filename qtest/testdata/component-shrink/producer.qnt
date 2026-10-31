module producer {
  var published: int
  action init = published' = 0
  action publish = published' = published + 1
  action noise = published' = published
  action step = any { publish, noise }
}
