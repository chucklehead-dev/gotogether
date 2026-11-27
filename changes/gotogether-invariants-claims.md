- `gotogether invariants -claims doc.md[,...]` lints a design document's
  marked claims (ROADMAP CW3's doc lint): a paragraph, list item or
  quoted line beginning `Claim:` (or `**Claim:**`), through its paragraph
  or item, must cite a property of the model, `(prop: name, ...)`, or an
  assumption it lists, `(assume: name)`; fenced code and unmarked prose
  are never read. A claim citing nothing, an empty citation, and a name
  that is no property or assumption of the model (or of the other kind,
  or the val that states an assumption), each fail (exit 1) at the
  claim's `file:line` with the fix. A cited property nothing enforces or
  checks, or a cited assumption nothing targets, is a note, and a `FAIL`
  under the new `-require cited`. Alone, `-claims` prints no table and
  `-require` takes `cited` only; beside `-write` or `-check` it lints as
  well. Each document's summary line is `ok   doc.md: N claims, citing P
  properties and A assumptions of model M` or `FAIL doc.md: N problems
  in its claims, ...`.
