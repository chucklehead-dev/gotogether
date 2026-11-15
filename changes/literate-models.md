- A binding's `spec:` may name a Markdown document: its ```` ```quint ````
  blocks, in order, are the model (ROADMAP CW1, L8; golem's and Goo's
  ask). Loading the binding writes them to the `.qnt` beside the document,
  stamped with the document's digest and a line map, never over a file
  gotogether did not generate. quint's errors, lint's findings and the
  reports name the document's lines. `gotogether tangle [-check]` writes
  the model, or fails in CI when a checked-in one is missing or stale.
  `binding.Binding.DocPath` is the spec as the binding names it.
- Each model check in `gotogether.report/2` carries `source`, where its
  definition is (`counter.md:23`, or the `.qnt`'s line), and a failing
  invariant's line in `simulate`'s and `validate`'s text names it.
