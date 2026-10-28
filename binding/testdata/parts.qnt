// A model whose commands share a part: appendTo, which each calls and
// step does not, a dispatcher whose steps are named through it, and a
// frame condition.
module parts {
  var log: List[int]
  var n: int

  action appendTo(x: int): bool = all { log' = log.append(x), n' = n + 1 }
  action same: bool = all { log' = log, n' = n }
  action add(x: int): bool = appendTo(x)
  action bump: bool = appendTo(0)
  action idle: bool = same
  action dispatch(x: int): bool = any { add(x), bump }

  action init = all { log' = [], n' = 0 }
  action step = any { nondet x = 1.to(3).oneOf() dispatch(x), idle }
}
