- `simulate -matrix` fixes from golem's adoption (#144):
  - a variant's own `search: { depth, states }` is what its graph and
    possibles search; every variant searched the section's bound;
  - a holds proved only to a depth reads "holds to depth N" in the table,
    and the JSON cell carries `depth` (`model.MatrixCell.Depth`,
    `validate.MatrixCellDoc.Depth`; 0 for the whole state graph), where it
    read "holds" as a whole graph does;
  - `-process-memory` returns freed pages to the operating system before
    confirming a reading past its bound, so an earlier variant's freed graph
    no longer cuts the next one (it cut one at 113 states), and
    `processSeen` keeps only readings within the bound or confirmed.
