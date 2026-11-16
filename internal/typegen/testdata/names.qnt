// Names gotogether types must rename, and types it keeps as itf.Value
// (internal/typegen's TestNames).
module lib {
  type Item = { a: int }
}

module names {
  import lib as l
  type Option[a] = Some(a) | None
  // the model's own State: the state's struct is State_
  type State = { n: int }
  // Item here keeps its name; lib's is Item_
  type Item = { b: int }
  // a type of the name Mode's constant Fast would have: the constant is ModeFast_
  type ModeFast = { x: int }
  type Mode = Fast | Slow
  // one name to itf.Decode: itf.Value
  type Shape = A_b(int) | Ab(str)
  // a variant named Tag: its field is Tag_
  type Cmd = Tag(int) | Run
  // fields that collide once exported, and Go keywords (exported, no keyword)
  type Fields = { ab: int, Ab: int, _hidden: int, hidden: str, func: bool, go: int }
  // a record key: a Go map
  type Point = { x: int, y: int }
  // an application of Option, declared: Option[str] elsewhere is not it
  type MaybeItem = Option[Item]

  var st: State
  var shape: Shape
  var cmd: Cmd
  var rec: Fields
  var li: l::Item
  var it: Item
  var mode: Mode
  var fast: ModeFast
  var byOpt: Option[int] -> str
  var byPoint: Point -> int
  var maybe: MaybeItem
  var note: Option[str]

  action init = all {
    st' = { n: 0 },
    shape' = Ab("x"),
    cmd' = Run,
    rec' = { ab: 1, Ab: 2, _hidden: 3, hidden: "h", func: true, go: 4 },
    li' = { a: 1 },
    it' = { b: 1 },
    mode' = Fast,
    fast' = { x: 1 },
    byOpt' = Map(None -> "none"),
    byPoint' = Map({ x: 1, y: 2 } -> 3),
    maybe' = Some({ b: 1 }),
    note' = Some("n"),
  }

  action step = all {
    st' = st, shape' = shape, cmd' = cmd, rec' = rec, li' = li, it' = it,
    mode' = mode, fast' = fast, byOpt' = byOpt, byPoint' = byPoint,
    maybe' = maybe, note' = note,
  }
}
