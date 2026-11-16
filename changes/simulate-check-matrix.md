- `simulate -matrix -write-matrix doc.md` no longer leaves a literate
  document's generated `.qnt` stale: writing the table changed the
  document's digest and line map, so `tangle -check` failed after it
  (Goo, #220). A `.qnt` current before the write is written again after
  it; one already stale or missing is left for `tangle -check`.
- `simulate -matrix -check-matrix doc.md` compares the matrix with the
  document's table, writing nothing, and exits 1 when they differ: the
  check for CI.
