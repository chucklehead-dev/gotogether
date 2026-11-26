- `qtrace.Linearize` with pending calls (`qtest.ResolvePending` with
  `qtest.Linearize`): the complete search keeps, of two accepted prefixes of
  the same steps and state, the one that placed only some of the other's
  pending calls (it may leave the rest out, or place them later), as it
  kept one of two with the same calls. A pending read, or a pending write a
  later write overwrote, placed, no longer doubles the prefixes searched:
  examples/raftkv's seed 20783303 (five pending calls) conforms in 5741
  orders, where 20000 were not enough. `Proof.Merged` may pair a prefix with
  a longer one that placed more pending calls; an oracle confirms it as
  before (both accepted, one state). An `OrderCheck`'s states must keep
  everything a later judgment reads, as documented: a ghost history whose
  pending writes all must land belongs in the state.
