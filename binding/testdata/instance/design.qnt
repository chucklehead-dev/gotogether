// A parameterised model and its designs as instance modules, the style
// the skills recommend: a binding names a design, not the base module.
module counter {
  const LIMIT: int
  const OVERSHOOT: bool

  var n: int

  action init = n' = 0

  action add(k: int): bool = all {
    k > 0,
    if (OVERSHOOT) n + k <= LIMIT + 1 else n + k <= LIMIT,
    n' = n + k,
  }

  action step = nondet k = 1.to(2).oneOf() add(k)

  val withinLimit: bool = n <= LIMIT
}

// The design as built.
module design {
  // the limit, and the switch a broken design flips
  import counter(LIMIT = 3, OVERSHOOT = false).*
}

/* a broken design */ module overshoot { import counter(LIMIT = 3, /* the switch */ OVERSHOOT = true).* }

// Not an instance: it defines something beside its import.
module extended {
  import counter(LIMIT = 3, OVERSHOOT = false).*
  val twice: int = 2 * n
}
