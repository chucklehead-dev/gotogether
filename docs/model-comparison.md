# Comparing model behavior

`gotogether model-diff` compares two finite model instances on an explicit list
of observed variables. Use it to review a changed guard or a model mutant before
trusting it to judge code. It does not infer the intended contract: an added
behavior might be a deliberate feature or an accidentally weakened model.

```sh
gotogether model-diff -before old.binding.yaml -after new.binding.yaml \
  -observe phase,attempts -states 1000 -pairs 10000 -json
```

Both bindings must expose the same named observation variables. The nonempty
`-observe` list is required; duplicates and missing variables are errors. A shared
`-const NAME=expr,...` selects the finite instance, overriding each binding's
constants. The report records the compared bindings, modules, constants,
projection, graph bounds and each directional comparison's extent.

The compared language consists of every finite sequence of projected states,
including the initial state and one observation after every transition. Action
names, fairness and infinite-run liveness are not compared. A hidden variable
updated alongside existing transitions can leave behavior unchanged. A new
hidden-only transition adds an observable repeated state and can change behavior:
there is no implicit stutter hiding or refinement mapping.

The product search tracks a concrete source state and every target state still
compatible with the entire observed prefix. This matters for hidden
nondeterminism: matching each projected edge separately can invent a target path
by combining incompatible hidden states. The two directions are separate:
`added` is behavior admitted after the change but not before; `removed` reverses
the models. Witnesses are shortest by number of model transitions.

The repository acceptance case is a finite handshake with one optional retry.
Compare `validate/model/testdata/compare/before.yaml` with `after.yaml` to find
an acknowledgment admitted before sending; compare `before.yaml` with `ghost.yaml`
on `phase,retries` for unchanged observed behavior despite internal updates.

Both graphs must be complete before this slice compares them. A state or depth
cut, unsupported evaluator construct, or model runtime error is inconclusive.
`-pairs` separately caps the product nodes in each directional inclusion search.
`-work` caps target-state candidate processing; the report records its actual
usage and identifies which budget cut a direction. Target subsets are shared
internally rather than copied for every source state.
Exhausting that budget is also inconclusive, never evidence of equivalence.
An equivalence result applies to these finite instances, these projected state
sequences, and the Go evaluator's supported semantics. It is not a proof over
arbitrary constants or a general refinement proof.

A witness retains its projected sequence, concrete source states and an exact
Quint replay. Native replay checks original `init` and `step`, with every full
state asserted. A successful replay confirms source admission. Exclusion rests
on the complete target graph and product search; a failed nondeterministic native
sample cannot establish target rejection. If no candidate witness can be confirmed
natively, the comparison remains inconclusive and retains the explicit evidence.
The report keeps these two evidence bases separate.

Text and `-json` derive from the same comparison result. JSON uses the current
`gotogether.report/2` document's typed `comparison` field rather than a second
format. Exit 0 means equivalent for the explicit instances, 1 means a difference
with source replay evidence, and 2 means inconclusive or an operational error.
Inspect per-direction statuses and native replay evidence, not just the exit code.

## Keeping a confirmed witness

Add `-out review` to publish a fresh directory containing `report.json`, a Quint
module for each source-confirmed witness, and its copied model sources. Export
refuses an existing destination. With `-out`, use `-json` for stdout; separate
`-report` destinations and `GOTOGETHER_REPORT_DIR` are refused so a second writer
cannot overwrite exported evidence. Without `-out`, the usual report renderings
remain available. Source guards and hashes are checked before
publication; failure leaves no partially published evidence directory.

The witness's typed `replay` object carries the module hash, copied source hashes
and exact Quint argument vector. Paths in that object are relative to the export
directory. Choose a Quint executable, change into the exported directory, and
execute that argument vector. It retains the seed that actually reproduced the
full source path. The original source tree can then be removed and the directory
relocated without reconstructing instance imports or constants.

Only source-confirmed witnesses receive replay artifacts. An equivalent or
inconclusive comparison still exports its report; unconfirmed candidates are not
upgraded by being written to disk. This preserves native source admission, not
the target graph or a new proof of target exclusion. The report's original binding
and model identities remain provenance; rerunning the entire comparison requires
its original inputs. Absolute model imports and incomplete captured source
inventories are refused for portable replay.

## A real model change outside this equivalence

The [historical hello review](../internal/adoptiontest/model-review/README.md)
uses exact bindings and models from before and after commit `5ead7582`. That
change added a `done` action and occurrence claims after mutation testing exposed
a client that stopped before applying every request. Both eight-state instances
have the same observed `applied,count` sequences, with 24 and 25 transitions.
The new completed-state stutter was already possible through `replay`.

An equivalent result is therefore correct and does not approve the completion
contract change. Retain action-occurrence and completion checks when reviewing
such changes. The rehearsal checks the explicit scope and preserves the original
sources; it does not alter a guard or add a bound to make historical models fit.
