- A binding's compiled model is kept gzipped by convention:
  `validate.CompiledFor` (and so `gotogether compile -binding B`, qtest's
  `RequireModel` and `GOTOGETHER_COMPILED`, `gotogether doctor`) names
  `<stem>.model.json.gz` (`validate.CompiledExt`), no longer
  `<stem>.model.json`. The format is still `gotogether.model/1`: `Write`
  gzips a path ending in `.gz`, with a header naming no file, time or OS,
  so a compile twice writes the same bytes; `LoadCompiled`, `validate
  -compiled` and `doctor -compiled` read the plain and the gzipped form
  alike, and refuse a broken container with the fix. An artifact kept
  plain beside its binding is not found by the convention: compile it
  again (DESIGN §113). Every example binding now keeps its compiled model,
  within the 1 MB budget compressed.
- `gotogether compile -check` no longer calls a model stale whose quint
  typecheck or IR holds `<`, `>` or `&`: `CompiledModel.Differs` compares
  the documents with JSON's HTML escapes alike (an artifact written holds
  them escaped, quint's output does not).
