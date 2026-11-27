- `gotogether invariants -claims`, golem's and Goo's first use (#144, #220):
  - A claim's error lines start with `FAIL`, as its unbacked citations'
    do under `-require cited`. A citation naming what the model lacks
    ends `; or, if the document no longer makes this claim, drop Claim:`
    (a superseded claim is fixed by its marker, not the model).
  - A citation qualified by a module the spec does not have
    (`(prop: publication.revocationBounded)` in the run of exposure) is
    another model's: noted once a document (`note: doc.md: N citations of
    other models (...), left out of the claims of model M: publication
    (N)`) and left out, as such annotations are. A claim citing only other
    models is left to their runs, and the summary says so (`ok   doc.md:
    3 claims, citing ... of model M; 1 claim cites only other models
    (publication), left to their runs`). Each model's run, given the same
    documents, checks its own claims.
  - `Claim (model):` (or `**Claim (model):**`) marks a claim about the
    model alone: its citations are checked against the model, and
    `-require cited` does not ask the code to back them (its gaps are
    notes, `(a model-only claim: ...)`). The summary counts them
    (`3 claims (1 model-only), citing ...`). `Claim (other):` is an
    error naming the two markers.
  - A cited val the table's selection leaves out names the selection that
    did (`the binding does not check it`, `-exclude names it`, `-props
    does not name it`, `-invariant's conjuncts do not include it`), and
    suggests `Claim (model):` for a claim about the model alone; a
    directive naming such a val names it the same way.
- `gotogether invariants`: the fixes that suggest a directive
  (`//quint:enforces`, `//quint:checks`, `//quint:exercises`,
  `//quint:monitors`, in `-require` failures and claims' notes) qualify
  the name by the main module (`exposure.knownSpendCurrent`) when the
  scanned code qualifies any of its annotation names, so the suggested
  line does not break another model's run.
- `//quint:monitors name label:VALUE` gives a monitor the metric or alert
  it reports (`label:golem.control.spend.stale`, or quoted,
  `label:"Checkpoint tampered"`), shown in the assumptions table before
  its function: `` `golem.control.spend.stale` in `metrics.Register` ``.
  A monitor on a statement with no label is shown by the identifier the
  statement assigns (`spendStale := ...`). Only `//quint:monitors` takes
  a label.
