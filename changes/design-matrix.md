- `simulate.variants` lists a design's alternatives (`binding.Variant`: a
  name, constants over the section's, and what it holds and allows), and
  `gotogether simulate -matrix` checks each and prints the table, properties
  by variants, each cell what the run found (`breaks in 2 steps`, `holds`)
  and `FAIL` where the variant says otherwise (ROADMAP CW2, golem's design
  comparisons). `-write-matrix doc.md` writes the table into a document
  between its `<!-- gotogether:matrix -->` lines; `-json` prints
  `gotogether.matrix/1` (`validate.MatrixDocument`, `model.Matrix`).
- In a matrix, a variant whose invariants it must all break (no eventual
  val) stops its graph once each has broken (`qeval.Model.ExploreUntil`,
  `model.GraphSearch.UntilBroken`, cut `broken`), and a variant's own
  `search` bounds its searches.
