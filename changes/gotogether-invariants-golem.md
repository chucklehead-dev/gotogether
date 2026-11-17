- `gotogether invariants`, from golem's adoption across three models
  (#144):
  - `-props a,b` (exactly these, in this order), `-invariant safe` (the
    val's conjuncts, expanded through conjunctions of vals) and
    `-exclude a,b` choose the properties; `-props` and `-invariant`
    replace a binding's set, `-exclude` removes from it, and a name that
    is no boolean val is refused with the nearest.
  - Named markers, `<!-- gotogether:invariants NAME -->` (and
    `actions`), hold several models' tables in one document: a run
    writes and checks only the pair of its `-name` (default the main
    module, or the binding's module), else the unnamed pair.
  - A name qualified by a module the spec does not have is another
    model's: left out and counted in one `note:` line, so one tree can
    carry several models' qualified annotations. Actions are not
    qualifiable.
  - `-links name` shows each site by package and function, linked to its
    file without a line, so moved code no longer fails `-check`.
  - A meaning is the first sentence of the doc comment's first
    paragraph, its wrapped lines joined, not its first line.
  - Sites are qualified by package (`state.Restore`) when the tables span
    packages; a `checks` directive in a `t.Run` closure with a literal
    name names the subtest (`TestReset/from_the_cap`).
  - `-literate=false` refuses a `.md` spec (name the `.qnt` another
    tangler writes) and writes the document as plain Markdown.
  - An annotation naming a val the table leaves out now reads `... is a
    val of model M that this table leaves out (...)`, and a document with
    no table of the model `... has no table of model M: add ...`.
