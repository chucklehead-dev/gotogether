- `simulate -matrix` releases each variant's graph before the next
  variant searches: every search ran beside the graphs before it, so a
  matrix of large variants compounded (golem's leadership, #144: from the
  twelfth of 30 variants each search was cut by memory at depth 2 to 4).
  Four identical variants of 100,000 states read 378, 557, 738 and 889 MB;
  they now read about 350 MB each.
- `simulate -matrix -variant NAME` runs one variant alone
  (`model.SimulateOptions.Variant`), so a script can run each in its own
  process and in parallel; it refuses `-write-matrix`, which would write
  one column over the document's table.
