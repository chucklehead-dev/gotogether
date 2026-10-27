// Names as a page records them: strings with tabs, backslashes and
// newlines, which a Quint literal writes as they are (no escapes).
module strs {
  var names: List[str]

  action init = names' = []

  action named(name: str): bool = names' = names.append(name)

  action step = named("a")
}
