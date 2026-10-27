// Two modules of one file, each with its own Event, and a third type two
// imported modules define differently: toQuint reads the model's own
// Event, and refuses the ambiguous one.
module lib {
  type Event = { id: int }
  type Shared = { a: int }
}

module other {
  type Shared = { b: str }
}

module twomods {
  import lib.Shared
  import other as O
  type Event = { name: str }

  var last: str

  action init = last' = ""

  action seen(e: Event, s: Shared, o: O::Shared, x: a): bool = last' = e.name

  action step = seen({ name: "n" }, { a: 1 }, { b: "" }, 1)
}
